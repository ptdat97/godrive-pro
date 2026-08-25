.PHONY: run worker test test-race test-integration lint fmt vet build migrate-up migrate-down up down tidy infra-up infra-down

APP := godrive

run: ## Chạy API ở chế độ dev (in-memory, không cần Postgres)
	go run ./cmd/api

worker:
	go run ./cmd/worker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/worker ./cmd/worker

test:
	go test ./... -count=1

test-race:
	go test ./... -race -count=1

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal ./pkg

tidy:
	go mod tidy

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1

up:
	docker compose -f deploy/docker-compose.yml up -d

down:
	docker compose -f deploy/docker-compose.yml down

## Hạ tầng cho môi trường phát triển
infra-up:
	docker compose -f deploy/docker-compose.yml up -d postgres redis nats emqx

infra-down:
	docker compose -f deploy/docker-compose.yml down

## Chạy đủ bộ test, gồm cả test tích hợp với hạ tầng thật.
## Các biến TEST_* dùng riêng, KHÔNG dùng lại biến của ứng dụng: test xoá sạch
## dữ liệu trước khi chạy nên không bao giờ được trỏ nhầm vào môi trường thật.
test-integration:
	TEST_DATABASE_URL="$${TEST_DATABASE_URL:-postgres://postgres@localhost:5432/godrive_test?sslmode=disable}" \
	TEST_REDIS_URL="$${TEST_REDIS_URL:-redis://localhost:6379/15}" \
	TEST_NATS_URL="$${TEST_NATS_URL:-nats://localhost:4222}" \
	TEST_MQTT_URL="$${TEST_MQTT_URL:-tcp://localhost:1883}" \
	go test ./... -race -count=1
