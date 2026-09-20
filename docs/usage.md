# Usage and deployment

## Docker Compose

Run the service from the repository root:

```bash
docker compose up -d --build --wait
docker compose ps
curl http://127.0.0.1:18080/
curl http://127.0.0.1:18080/8.8.8.8
curl 'http://127.0.0.1:18080/2001:4860:4860::8888'
curl -X POST http://127.0.0.1:18080/ \
  -H 'Content-Type: application/json' \
  --data '["8.8.8.8", "1.1.1.1"]'
```

The first build downloads the Go base image and module dependencies. It reuses locally verified databases when available; otherwise, it downloads approximately 46 MiB of database files. The pinned upstream commit and SHA-256 checksums are stored in `data/upstream-ref` and `data/SHA256SUMS`. Subsequent builds can reuse Docker layer cache. Run `make db` before building if you prefer to download the databases explicitly.

By default, Compose maps `127.0.0.1:18080` on the host to port `8080` in the container. Create a local environment file to change the host binding, port, or log level:

```bash
cp .env.example .env
docker compose up -d --wait
```

Set `BIND_HOST=0.0.0.0` in `.env` to expose the service to other machines. Keep the container-level `HTTP_ADDR` set to `:8080`.

Inspect logs or stop the service with:

```bash
docker compose logs -f api
docker compose down
```

The container runs as a non-root user with a read-only root filesystem and a built-in readiness check. Compose retains up to three 10 MiB log files. On shutdown, the service allows up to 10 seconds for in-flight requests to complete.

## Dockerfile

The service can also run without Compose:

```bash
docker build -t ips:local .
docker run -d --name ips --read-only \
  -p 127.0.0.1:18080:8080 ips:local
```

The multi-stage build produces a `scratch`-based runtime image containing only the static executable, databases, and database provenance files. The running container makes no external API calls.

## Continuous image builds

The `docker` workflow in `.github/workflows/docker.yml` builds the image on pull requests and on pushes to `main` and `v*` tags. Pull request builds verify the Dockerfile only. Pushes publish the image to `ghcr.io/<owner>/<repository>` with branch, tag, semantic version, commit, and `latest` tags:

```bash
docker pull ghcr.io/lxneng/ips:latest
```

## Lookup API

```http
GET /
GET /{ip}
POST /
```

`GET /` uses the first address in `X-Forwarded-For` when the header is present and otherwise uses the client address of the current TCP connection. The forwarded address must be an IPv4 or IPv6 address without a port, CIDR prefix, or zone identifier. The service trusts this header directly, so an untrusted client can spoof the reported address. Deployments that require an authoritative client address must place the service behind a reverse proxy that removes incoming forwarding headers and sets `X-Forwarded-For` itself.

`GET /{ip}` looks up the address supplied in the path. It accepts IPv4, IPv6, and IPv4-mapped IPv6 addresses. Mapped addresses are normalized to IPv4. Hostnames, port numbers, CIDR prefixes, and IPv6 zone identifiers are rejected.

`POST /` performs multiple lookups in one request. The request body must use `Content-Type: application/json` and contain a non-empty JSON array of up to 1,000 IP address strings. The request body must not exceed 1 MiB. Each valid address is looked up independently. Successful and failed items are returned in separate arrays, each retaining relative input order and duplicate entries.

```bash
curl http://127.0.0.1:18080/
curl http://127.0.0.1:18080/8.8.8.8
curl 'http://127.0.0.1:18080/::ffff:8.8.8.8'
curl -X POST http://127.0.0.1:18080/ \
  -H 'Content-Type: application/json' \
  --data '["8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"]'
```

A successful lookup returns a flat JSON object. Location data depends on the pinned database version:

```json
{
  "ip": "8.8.8.8",
  "ip_version": 4,
  "country": "United States",
  "province": "California",
  "city": "",
  "isp": "Google LLC",
  "country_code": "US"
}
```

A valid batch request returns `200` with an overall status and separate success and failure arrays. Successful items use the same object structure as a single lookup; failed items contain the original input and an error. The status is `success` when every item succeeds, `partial_success` when both arrays contain items, and `failed` when every item fails.

```json
{
  "status": "partial_success",
  "successes": [
    {
      "ip": "8.8.8.8",
      "ip_version": 4,
      "country": "United States",
      "province": "California",
      "city": "",
      "isp": "Google LLC",
      "country_code": "US"
    }
  ],
  "failures": [
    {
      "ip": "invalid",
      "error": {
        "code": "invalid_ip",
        "message": "invalid IP address"
      }
    }
  ]
}
```

| Field | Description |
| --- | --- |
| `ip` | Normalized lookup address; for `/`, the first `X-Forwarded-For` address or the current connection's client address |
| `ip_version` | IP version, either `4` or `6` |
| `country` | Country or region name |
| `province` | First-level administrative subdivision |
| `city` | City name |
| `isp` | ISP or network label supplied by the database |
| `country_code` | Two-letter country or region code |

Database values equal to `0` are returned as empty strings. Locations in China are generally written in Chinese, while other locations are generally written in English. Private, reserved, and loopback addresses are passed to the database without inferred location data.

Every origin response includes an `X-Request-ID` header for correlation with structured service logs. A response served by an intermediary cache may retain the request ID of the origin response that populated the cache. The two GET endpoints support `HEAD`, which returns the same status and headers without a response body.

`GET /` varies by client and returns `Cache-Control: no-store`. A successful `GET /{ip}` lookup is deterministic for the loaded database and returns `Cache-Control: public, max-age=3600`. Batch responses, error responses, and health endpoints also use `no-store`.

Error responses use a consistent envelope:

```json
{"error":{"code":"invalid_ip","message":"provide one IPv4 or IPv6 address without a port, CIDR prefix, or zone"}}
```

Batch item errors use the same `code` and `message` fields alongside the original `ip` input. They do not change the batch HTTP status from `200`. Request-level failures still use the error envelope above.

| HTTP status | Error code | Meaning |
| --- | --- | --- |
| `400` | `invalid_request` | The batch body is malformed, empty, has the wrong JSON shape, or contains trailing JSON |
| `400` | `invalid_ip` | A single-lookup path parameter is not a supported IP address |
| `404` | `ip_not_found` | The address is absent from the database |
| `404` | `route_not_found` | The route does not exist, such as a multi-segment path |
| `405` | `method_not_allowed` | The endpoint does not accept the HTTP method |
| `413` | `batch_too_large` | The batch contains more than 1,000 items |
| `413` | `request_too_large` | The request body exceeds 1 MiB |
| `415` | `unsupported_media_type` | A batch request does not use `application/json` |
| `500` | `internal_error` | The lookup failed; details are recorded in service logs |
| `503` | `not_ready` | The service is starting or shutting down |

Batch items may report `invalid_ip`, `ip_not_found`, or `internal_error`. A panic or another failure that prevents the server from constructing a batch response still returns a request-level `500` error.

Any single path segment is interpreted as an IP address, so `/hello` returns `400` rather than `404`.

## Health endpoints

| Path | Purpose |
| --- | --- |
| `/healthz` | Liveness check; returns `200` while the HTTP process is responsive |
| `/readyz` | Readiness check; returns `200` after both databases are loaded and `503` during shutdown |

The process exits before opening its listener when a database is missing, has the wrong IP version, or contains an invalid index. The Docker health check invokes the executable's built-in `healthcheck` command against `/readyz`, so the runtime image does not need curl.

## Local execution and configuration

```bash
make db
make build
HTTP_ADDR=127.0.0.1:18080 ./bin/ips
```

Use `make run` as a shorthand for the same local workflow.

| Environment variable | Default | Description |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | Listen address; `make run` uses `127.0.0.1:18080` |
| `IPV4_DB_PATH` | `data/ip2region_v4.xdb` | IPv4 database path |
| `IPV6_DB_PATH` | `data/ip2region_v6.xdb` | IPv6 database path |
| `LOG_LEVEL` | `info` | One of `debug`, `info`, `warn`, or `error` |

Compose reads `.env`; the Go process reads its configuration from environment variables. Both IPv4 and IPv6 databases are required.

## Database updates

Download the pinned database version, or verify existing files against the recorded checksums:

```bash
make db
```

Fetch the current upstream HEAD and update the recorded commit and checksums:

```bash
make db-update
make check
docker compose up -d --build --wait
```

Update mode additionally requires git. A failed download or checksum mismatch never replaces existing database files. After an update, review changes to `data/upstream-ref` and `data/SHA256SUMS`, then commit them with the corresponding code changes. The process holds an in-memory snapshot, so it must be restarted to load replacement databases.

The upstream community database is updated irregularly. A current repository revision does not imply that every record reflects same-day routing or geolocation information. Use an upstream commercial database or maintain a compatible data source when predictable update frequency is required.

## Validation and dependency upgrades

```bash
make check
```

Unit tests cover address validation, IPv4-mapped IPv6 normalization, error handling, readiness state, configuration validation, cache policy, and corrupted databases. Integration tests load the real IPv4 and IPv6 databases and exercise boundary addresses, version mismatches, and concurrent lookups under Go's race detector.

Upgrade the Go binding with:

```bash
go get github.com/lionsoul2014/ip2region/binding/golang@latest
go mod tidy
make check
```

When upgrading Go, update both `go.mod` and `Dockerfile`. Source dependencies, database provenance, and deployment configuration are versioned independently so each change remains reviewable.
