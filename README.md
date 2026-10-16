# ИИ-бот учёта семейных расходов

Telegram-бот с LLM-разбором трат + веб-панель главы семьи. Бэкенд на Go, запуск одним `docker compose`.

## Запуск

```bash
cp .env.example .env   # заполнить TELEGRAM_TOKEN и LLM_*
docker compose up --build
```

Панель: http://localhost:8080

## Структура

```
cmd/bot, cmd/api        точки входа (два сервиса, общая БД)
internal/config         конфиг из env, включая доступ к LLM
internal/domain         сущности: User, Family, Expense, Category
internal/repository     интерфейсы хранилища + postgres-реализация
internal/service        бизнес-логика (траты, семьи, статистика)
internal/filter         префильтр сообщений до LLM, синонимы
internal/llm            клиент к OpenAI-совместимому API, промпт
internal/bot            Telegram-хендлеры
internal/httpapi        API веб-панели, авторизация через Telegram
internal/invite         генерация уникальных кодов семьи
internal/timeutil       часовые пояса
migrations              SQL-миграции
web/static              фронтенд панели
```
