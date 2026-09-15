.PHONY: db db-update run build fmt test test-integration vet check up down

db:
	sh scripts/download-db.sh

db-update:
	sh scripts/download-db.sh --update

run: db
	HTTP_ADDR=127.0.0.1:18080 go run ./cmd/ips

build:
	go build -trimpath -o bin/ips ./cmd/ips

fmt:
	gofmt -w cmd internal

test:
	go test -race ./...

test-integration: db
	go test -race -tags=integration ./internal/geo

vet:
	go vet ./...

check: test vet test-integration

up:
	docker compose up -d --build --wait

down:
	docker compose down
