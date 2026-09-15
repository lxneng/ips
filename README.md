# ips

A production-oriented, offline IP geolocation REST service built with Go and [ip2region](https://github.com/lionsoul2014/ip2region). It supports IPv4, IPv6, concurrent lookups, deterministic database builds, and container deployment.

## Quick start

```bash
docker compose up -d --build --wait
curl http://127.0.0.1:18080/
curl http://127.0.0.1:18080/8.8.8.8
curl 'http://127.0.0.1:18080/2001:4860:4860::8888'
curl -X POST http://127.0.0.1:18080/ \
  -H 'Content-Type: application/json' \
  --data '["8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"]'
```

The image build downloads and verifies the pinned databases when they are not already available locally. The running service is fully offline and listens on `127.0.0.1:18080` by default.

## Documentation

- [Usage and deployment](docs/usage.md)
- [Architecture and data provenance](docs/design.md)
- [OpenAPI specification](docs/openapi.yaml)

## Local development

Development requires Go `1.27.1`, curl, make, and either `sha256sum` or `shasum`.

```bash
make run
```

Run `make check` in another terminal to execute unit tests, static analysis, and race-enabled integration tests against the real databases.
