.PHONY: run worker test test-race lint fmt vet build migrate-up migrate-down up down tidy

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
