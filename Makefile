.PHONY: run build test test-race test-cover lint docker-up docker-down docker-logs clean

# ── Development ──────────────────────────────────────────────────────────────
run:
	go run ./cmd/server

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/server ./cmd/server
	@echo "Built: bin/server"

# ── Testing ───────────────────────────────────────────────────────────────────
test:
	go test ./... -v

test-race:
	go test ./... -v -race -timeout 120s

test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ── Linting ───────────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./...

vet:
	go vet ./...

# ── Docker ────────────────────────────────────────────────────────────────────
docker-up:
	docker-compose up -d --build

docker-down:
	docker-compose down -v

docker-logs:
	docker-compose logs -f app

docker-ps:
	docker-compose ps

# ── Database ──────────────────────────────────────────────────────────────────
migrate-up:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "DATABASE_URL is not set"; exit 1; \
	fi
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "DATABASE_URL is not set"; exit 1; \
	fi
	migrate -path migrations -database "$(DATABASE_URL)" down

# ── Cleanup ───────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/ coverage.out coverage.html

# ── Convenience ───────────────────────────────────────────────────────────────
deps:
	go mod tidy
	go mod verify

help:
	@echo "Available targets:"
	@echo "  run          - Start server locally (in-memory mode)"
	@echo "  build        - Build binary to bin/server"
	@echo "  test         - Run all tests"
	@echo "  test-race    - Run all tests with race detector"
	@echo "  test-cover   - Run tests and generate HTML coverage report"
	@echo "  lint         - Run golangci-lint"
	@echo "  vet          - Run go vet"
	@echo "  docker-up    - Start all services via Docker Compose"
	@echo "  docker-down  - Stop and remove containers"
	@echo "  docker-logs  - Tail app container logs"
	@echo "  migrate-up   - Apply SQL migrations (requires DATABASE_URL)"
	@echo "  migrate-down - Roll back SQL migrations"
	@echo "  clean        - Remove build artifacts"
