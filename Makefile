.PHONY: build run test vet lint fuzz metrics fmt tidy db up down logs prod backup-now

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

# Линтер в Docker: локальная установка golangci-lint не нужна. Конфиг - .golangci.yml.
lint:
	docker run --rm -v "$(CURDIR)":/app -v "$$(go env GOMODCACHE)":/go/pkg/mod -w /app golangci/golangci-lint:latest golangci-lint run ./...

# Короткий фаззинг парсера сумм и фильтра.
fuzz:
	go test ./internal/nlp/filter -run '^$$' -fuzz '^FuzzAnalyze$$' -fuzztime 30s
	go test ./internal/nlp/parse -run '^$$' -fuzz '^FuzzParseAmounts$$' -fuzztime 30s

# Метрики Prometheus работающего приложения.
metrics:
	curl -s localhost:$${APP_PORT:-8080}/metrics

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
	docker compose logs -f app web

# Деплой с HTTPS (Caddy) и ежедневными бэкапами. Нужны DOMAIN и PUBLIC_URL=https://DOMAIN в .env.
prod:
	docker compose --profile prod up -d --build

backup-now:
	mkdir -p backups && docker compose exec -T db pg_dump -U app --no-owner --clean --if-exists expenses | gzip > backups/expenses-manual-$$(date -u +%Y%m%d-%H%M%S).sql.gz
