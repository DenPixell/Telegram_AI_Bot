// Package llm - доступ к языковой модели через один интерфейс Client.
//
//   - client.go: Client, Request/Response, инструменты (function calling), Caller в контексте;
//   - openai.go: OpenAI-совместимый /chat/completions (OpenRouter, Gemini, Groq, Ollama, ...);
//   - resilience.go: ретраи с джиттером и Retry-After, circuit breaker с пробным запросом,
//     fallback-цепочка провайдеров, лимит параллелизма с очередью;
//   - usage.go: учёт каждой попытки (токены, задержка, успех) для квот;
//   - build.go: сборка цепочки из конфига;
//   - fake.go: скриптованный клиент для тестов.
package llm
