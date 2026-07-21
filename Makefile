.PHONY: build generate generate-check generate-module check-architecture run test test-race test-core-coverage test-notice-coverage test-generator test-integration test-compose lint fmt vet env migrate-up migrate-down compose-up compose-down

build:
	go build ./...

generate:
	go run ./cmd/campusctl generate modules
	go run ./cmd/campusctl generate openapi
	go generate ./...

generate-check:
	go run ./cmd/campusctl generate modules --check
	go run ./cmd/campusctl generate openapi --check
	./scripts/check-go-generated.sh

generate-module:
	@test -n "$(SCHEMA)" || (echo 'usage: make generate-module SCHEMA=schemas/<module>.yaml' && exit 1)
	go run ./cmd/campusctl generate module "$(SCHEMA)"

check-architecture:
	@if rg -n 'internal/infrastructure/mysql/query' internal/core internal/api; then \
		echo '核心层或 API 层禁止依赖 MySQL 生成包'; \
		exit 1; \
	fi

run:
	go run ./cmd/server

test:
	go test -cover ./...

test-race:
	go test -race ./...

test-core-coverage:
	./scripts/check-core-coverage.sh

test-notice-coverage:
	./scripts/check-notice-coverage.sh

test-generator:
	go test -coverprofile=/tmp/campus-generator-coverage.out ./internal/generator
	go test ./cmd/campusctl
	@coverage=$$(go tool cover -func=/tmp/campus-generator-coverage.out | awk '/^total:/ {gsub("%", "", $$3); print $$3}'); \
	awk -v coverage="$$coverage" 'BEGIN { if (coverage < 80) { printf "generator coverage %.1f%% is below 80%%\n", coverage; exit 1 } }' && \
	printf 'generator coverage %s%%\n' "$$coverage"

test-integration:
	go test -tags=integration ./tests/integration

test-compose:
	./scripts/test-compose.sh

lint:
	go tool golangci-lint run ./...

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

env:
	./scripts/init-env.sh

migrate-up:
	go run ./cmd/campusctl migration up

migrate-down:
	go run ./cmd/campusctl migration down 1

compose-up: env
	docker compose up -d --build

compose-down:
	docker compose down
