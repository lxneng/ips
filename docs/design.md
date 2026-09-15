# Architecture and data provenance

## Versions

The following versions were verified against their official sources on September 15, 2026:

| Component | Version or source |
| --- | --- |
| Go | Latest stable release: `1.27.1` |
| ip2region upstream release | `v3.18.0` |
| Go module | `github.com/lionsoul2014/ip2region/binding/golang` |
| Pinned Go module version | `v0.0.0-20260901011515-c1a1fc7d5941` |
| Pinned database commit | `c1a1fc7d5941760db3f8431dc05c48cf7f0e30a1` |
| Initial IPv4 database timestamp | 2026-08-28 04:43:01 UTC |
| Initial IPv6 database timestamp | 2026-08-14 05:38:41 UTC |

The Go binding lives in an upstream submodule and is therefore represented by a Go pseudo-version. Its pinned commit is also the target of the upstream `v3.18.0` tag. The HTTP layer uses the Go standard library; ip2region is the only direct module dependency.

Sources: [Go downloads](https://go.dev/dl/), [ip2region v3.18.0](https://github.com/lionsoul2014/ip2region/releases/tag/v3.18.0), and the [official Go binding](https://github.com/lionsoul2014/ip2region/tree/c1a1fc7d5941760db3f8431dc05c48cf7f0e30a1/binding/golang).

## Repository structure

```text
cmd/ips/             Process entry point, HTTP lifecycle, and container health check
internal/config/     Environment-based configuration and validation
internal/geo/        Database loading, structural validation, lookup, and field parsing
internal/httpapi/    Routing, responses, observability, and panic recovery
data/                Database provenance and checksums; binary databases are excluded from Git
scripts/             Database download and update workflow
docs/                Operations guide and OpenAPI specification
```

## Lookup and concurrency model

Both databases are loaded into memory at startup. The loader verifies the file format, IP version, index boundaries, and data pointers. Lookups perform no disk access or external network calls.

In the pinned upstream version, `xdb.Searcher.Search` writes to its internal `ioCount` field even in full-memory mode, so a searcher cannot be shared across concurrent requests. This service shares immutable database bytes and constructs a lightweight in-memory searcher for each lookup, avoiding shared mutable state. Race-enabled integration tests exercise concurrent lookups against the real databases. Database file handles are closed immediately after loading.

## Data contract

The official databases currently encode regions as five ordered fields:

```text
country-or-region|province-or-state|city|ISP|two-letter-country-or-region-code
```

The service maps those fields to explicit JSON properties and converts the upstream missing-value marker `0` to an empty string. Custom databases must use the same field order. A different field count causes the lookup to fail. Older five-field databases may use different semantics and are not assumed to be compatible solely because their field count matches.

IPv4-mapped IPv6 addresses are normalized to IPv4 so equivalent addresses return the same record. IPv6 zone identifiers such as `fe80::1%en0` only have meaning on a specific host and are rejected.

## Lifecycle and deployment

The process loads and validates both databases before opening the listener. A startup failure terminates the process. On SIGINT or SIGTERM, the instance becomes unready, stops accepting new connections, and allows up to 10 seconds for in-flight requests to finish.

The HTTP server enforces header, read, write, and idle timeouts. Requests are logged as structured JSON. Internal lookup failures and panic details are written only to service logs; clients receive a stable error envelope.

The Docker build pins the Go version tag, Go dependency version, database commit, and database checksums. It compiles only after checksum validation and runs unit and real-database integration tests during the build. A Go base-image upgrade requires updating its version tag.
