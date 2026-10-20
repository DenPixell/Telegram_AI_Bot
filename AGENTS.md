# AGENTS.md

Инструкции для ИИ-ассистентов, работающих с этим репозиторием. Спецификация проекта:
`docs/architecture.md` (архитектура v2). Читать её перед любой фичей.

## Структура

```
cmd/server/            единственный бинарник: REST API + Telegram-бот
internal/config        ENV -> Config, валидация при старте
internal/domain        сущности и ошибки, без зависимостей
internal/service       бизнес-правила, единственная точка мутаций, принимает domain.Actor;
                       хранилище видит только через свои интерфейсы (store.go)
internal/storage/postgres  pgx, миграции goose (embed, advisory lock)
internal/httpapi       REST панели: DTO, сессии, CSRF, маппинг ошибок
internal/telegram      long polling, дедуп, исходящий лимитер
internal/onboarding    FSM регистрации
internal/limits        rate limit на пользователя, суточные квоты LLM
internal/nlp/          разбор текста до LLM, по пакету на задачу:
  textnorm             нормализация, токены, основы слов
  parse                суммы и даты
  lexicon              словарь config/synonyms.yaml, действия, стоп-слова
  thesaurus            клиент Викисловаря
  category             гибридная категория: словарь, кэш, тезаурус
  intent               служебные команды без LLM
  filter               решение по сообщению: отказ, уточнение, трата, агент
internal/agent         tools, промпт, рендер ответов по шаблонам
internal/timezone      разбор часовых поясов
internal/llm           OpenAI-совместимый клиент, fallback, breaker, fake
config/synonyms.yaml   словарь синонимов, стоп-слова, лексикон действий
api/openapi.yaml       контракт REST API
web/                   панель: React + Vite + TS, nginx; сборка и dev только в Docker (node:22-alpine)
deploy/                Dockerfile бэкенда, Caddyfile, скрипт бэкапа
```

## Правила

- Стандартная библиотека в приоритете. Новая зависимость только с обоснованием.
- Интерфейсы объявляет потребитель (пакет, который их использует), реализация возвращает конкретный тип.
  `service` не импортирует `storage/postgres`, связывание - в `cmd/server`.
- Пакет - одна ответственность. Если пакет растёт за 6-8 файлов логики или меняется по разным причинам, делить.
- `context.Context` первым аргументом. Ошибки оборачивать через `fmt.Errorf("...: %w", err)`.
- `user_id` и `family_id` берутся **только** из Telegram-апдейта или сессии. Никогда из ответа модели или тела запроса.
- Каждый SQL-запрос к пользовательским данным содержит `family_id` (и `user_id` для бота).
- Деньги: `domain.Money` (копейки, `int64`), в JSON строка `"350.00"`. Никаких float.
- `spent_at` это `domain.Date` (локальная дата пользователя). Сервер и БД в UTC.
- Ответ пользователю формирует Go по шаблонам из фактического результата. Модель не пишет свободный текст.
- Текст сообщений пользователей и коды приглашений не логировать на уровне info.
- Логи: `log/slog`.
- Новая миграция: новый файл `internal/storage/postgres/migrations/NNNNN_name.sql`, старые не редактировать.
- Каждый шаг с тестами (table-driven) и отдельным коммитом.

## Команды

```bash
make test     # go test -race ./...
make vet
make lint     # golangci-lint
make run      # БД в docker, приложение через go run
make up       # всё через docker compose
make prod     # + HTTPS (Caddy) и бэкапы
```

Проверка панели без локального Node:
`docker run --rm -v "$PWD/web":/app -w /app node:22-alpine npm run build`.
