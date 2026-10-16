package llm

import (
	"context"
	"encoding/json"
)

// Client - один запрос к модели. Реализации: OpenAI-совместимый HTTP-клиент, обёртки
// (ретраи, breaker, fallback, лимит параллелизма, учёт токенов) и Fake для тестов.
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
	// Name - провайдер и модель для логов и учёта: "openrouter.ai/openrouter/free".
	Name() string
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // ответ ассистента с вызовами инструментов
	ToolCallID string     // для RoleTool: на какой вызов это результат
}

// Tool - описание инструмента (function calling). Parameters - JSON Schema объекта.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall - вызов инструмента моделью. Arguments - JSON как его прислала модель:
// валидирует его вызывающий код, модели не доверяем.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolChoice string

const (
	ToolChoiceAuto     ToolChoice = "auto"
	ToolChoiceRequired ToolChoice = "required" // модель обязана вызвать инструмент
	ToolChoiceNone     ToolChoice = "none"
)

type Request struct {
	Messages    []Message
	Tools       []Tool
	ToolChoice  ToolChoice // пусто - не передаётся
	MaxTokens   int        // 0 - значение клиента по умолчанию (LLM_MAX_OUTPUT_TOKENS)
	Temperature *float64   // nil - по умолчанию провайдера
	JSONMode    bool       // response_format=json_object (AGENT_MODE=json)
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type Response struct {
	Message      Message // RoleAssistant: Content и/или ToolCalls
	FinishReason string
	Usage        Usage
	Model        string // модель, которая фактически ответила
	Provider     string // Name() клиента, который ответил (важно при fallback)
}

// Caller - чей это запрос, для учёта токенов и квот. Кладётся в контекст.
type Caller struct {
	UserID, FamilyID int64
}

type callerKey struct{}

func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func CallerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey{}).(Caller)
	return c
}

// Float - указатель на число для Request.Temperature.
func Float(v float64) *float64 { return &v }
