.PHONY: build run test vet lint fmt tidy db up down logs

-include .env
export

build:
	go build -o bin/server ./cmd/server

# Локальный запуск: БД из compose, приложение через go run (DB_DSN с localhost).
run: db
	DB_DSN=postgres://app:app@localhost:$${DB_HOST_PORT:-5432}/expenses?sslmode=disable go run ./cmd/server

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

db:
	docker compose up -d db

up:
	docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f app
