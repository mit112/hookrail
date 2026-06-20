.PHONY: build test itest lint up down seed e2e chaos k8s-e2e
build: ; go build ./...
test: ; go test ./... -race -count=1
itest: ; go test -tags integration ./... -race -count=1
lint: ; golangci-lint run ./...
up: ; docker compose -f deploy/compose/docker-compose.yml up -d --build
down: ; docker compose -f deploy/compose/docker-compose.yml down -v
seed: ; docker compose -f deploy/compose/docker-compose.yml run --rm api hookrail-ctl seed
e2e: ; go test -tags e2e ./test/e2e -v -count=1
chaos: ; go test -tags chaos ./test/chaos -v -count=1 -timeout 40m

.PHONY: py-lint py-typecheck py-test py-verify py-build py-e2e
py-lint: ; cd clients/python && uv run ruff check . && uv run ruff format --check .
py-typecheck: ; cd clients/python && uv run mypy src
py-test: ; cd clients/python && uv run pytest -q -m "not e2e"
py-verify: py-lint py-typecheck py-test
py-build: ; cd clients/python && uv build && uv run --with twine python -m twine check dist/* && bash scripts/py-install-smoke.sh
py-e2e: ; bash clients/python/scripts/py-e2e.sh

.PHONY: web-verify web-build dashboard-assets
web-verify:
	cd clients/web && npm ci && npm run typecheck && npm run lint && npm run test && npm run build
web-build:
	cd clients/web && npm ci && npm run build
dashboard-assets: web-build
	cp -r clients/web/dist internal/dashboard/dist
web-e2e: ; ROOT=$(CURDIR) bash scripts/web-e2e.sh && test "$$(docker compose -f deploy/compose/docker-compose.yml ps -q | wc -l | tr -d ' ')" = "0"
k8s-e2e: ; bash scripts/k8s-e2e.sh
