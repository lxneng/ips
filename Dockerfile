FROM golang:1.27.1 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY data/ ./data/
COPY scripts/download-db.sh ./scripts/download-db.sh
RUN sh scripts/download-db.sh

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN go test ./... && go test -tags=integration ./internal/geo
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ips ./cmd/ips

FROM scratch
WORKDIR /app
COPY --from=build /out/ips /app/ips
COPY --from=build /src/data/ip2region_v4.xdb /src/data/ip2region_v6.xdb /app/data/
COPY --from=build /src/data/upstream-ref /src/data/SHA256SUMS /app/data/

ENV HTTP_ADDR=:8080 \
    IPV4_DB_PATH=/app/data/ip2region_v4.xdb \
    IPV6_DB_PATH=/app/data/ip2region_v6.xdb \
    LOG_LEVEL=info
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD ["/app/ips", "healthcheck"]
ENTRYPOINT ["/app/ips"]
