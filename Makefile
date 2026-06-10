.PHONY: build test itest lint up down seed e2e
build: ; go build ./...
test: ; go test ./... -race -count=1
itest: ; go test -tags integration ./... -race -count=1
lint: ; golangci-lint run ./...
up: ; docker compose -f deploy/compose/docker-compose.yml up -d --build
down: ; docker compose -f deploy/compose/docker-compose.yml down -v
seed: ; docker compose -f deploy/compose/docker-compose.yml run --rm api hookrail-ctl seed
e2e: ; go test -tags e2e ./test/e2e -v -count=1
