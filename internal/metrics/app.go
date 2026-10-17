package metrics

// Метрики приложения. Метки - только с ограниченным набором значений (провайдер, вердикт,
// шаблон маршрута), без user_id и текстов: иначе число рядов растёт без предела.
var (
	LLMRequests = NewCounterVec("llm_requests_total",
		"HTTP-запросы к LLM-провайдерам (включая повторы).", "provider", "result")
	LLMLatency = NewHistogramVec("llm_request_duration_seconds",
		"Длительность одного запроса к LLM.", DurationBuckets, "provider")
	LLMTokens = NewCounterVec("llm_tokens_total",
		"Токены LLM по типу.", "provider", "kind")

	FilterDecisions = NewCounterVec("filter_decisions_total",
		"Решения фильтра сообщений: reject, need_amount, expense (без LLM), agent (в LLM).", "verdict", "reason")
	CategorySource = NewCounterVec("category_source_total",
		"Откуда взята категория: dictionary, learned, online, none.", "source")

	TelegramUpdates = NewCounterVec("telegram_updates_total",
		"Апдейты Telegram: processed, duplicate, failed.", "result")
	TelegramSendErrors = NewCounterVec("telegram_send_errors_total",
		"Ошибки отправки сообщений по коду Bot API.", "code")

	HTTPRequests = NewCounterVec("http_requests_total",
		"HTTP-запросы к API по шаблону маршрута и статусу.", "method", "route", "status")
	HTTPDuration = NewHistogramVec("http_request_duration_seconds",
		"Длительность HTTP-запросов к API.", DurationBuckets, "route")
)
