# AGENTS.md

Инструкции для ИИ-ассистентов, работающих с этим репозиторием. Спецификация проекта:
`docs/architecture.md` (архитектура v2). Читать её перед любой фичей.

## Структура

```
cmd/server/            единственный бинарник: REST API + Telegram-бот
internal/config        ENV -> Config, валидация при старте
internal/domain        сущности и ошибки, без зависимостей
internal/service       бизнес-правила, единственная точка мутаций, принимает domain.Actor
internal/storage/postgres  pgx, миграции goose (embed, advisory lock)
internal/httpapi       REST панели: DTO, сессии, CSRF, маппинг ошибок
internal/telegram      long polling, дедуп, исходящий лимитер
internal/onboarding    FSM регистрации
internal/pipeline      лимиты, служебные намерения, фильтр, синонимы
internal/agent         tools, промпт, рендер ответов по шаблонам
internal/timezone      разбор часовых поясов
internal/llm           OpenAI-совместимый клиент, fallback, breaker, fake
config/synonyms.yaml   словарь синонимов, стоп-слова, лексикон действий
api/openapi.yaml       контракт REST API
```

## Правила

- Стандартная библиотека в приоритете. Новая зависимость только с обоснованием.
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
```
