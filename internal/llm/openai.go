package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OpenAIConfig - любой провайдер с OpenAI-совместимым /chat/completions:
// OpenRouter, Gemini (OpenAI endpoint), Groq, Cerebras, Mistral, GitHub Models, Ollama.
type OpenAIConfig struct {
	BaseURL   string // https://openrouter.ai/api/v1
	APIKey    string // пусто для Ollama
	Model     string
	Timeout   time.Duration // на один HTTP-запрос
	MaxTokens int           // по умолчанию для Request.MaxTokens = 0
	// ReasoningEffort для «думающих» моделей (Gemini 3.x, gpt-5, o-серия): "none", "minimal",
	// "low", ... Без него модель может потратить весь лимит токенов на размышления и не
	// вызвать инструмент. Пусто - параметр не передаётся (модели без reasoning его отвергают).
	ReasoningEffort string
}

type OpenAI struct {
	cfg    OpenAIConfig
	name   string
	client *http.Client
}

func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	host := cfg.BaseURL
	if u, err := url.Parse(cfg.BaseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return &OpenAI{
		cfg:  cfg,
		name: host + "/" + cfg.Model,
		// Прокси берётся из HTTPS_PROXY (http.ProxyFromEnvironment в DefaultTransport).
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *OpenAI) Name() string { return c.name }

// Ответ ограничен: вызов инструмента короткий, мегабайт с запасом.
const maxResponseBytes = 1 << 20

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"` // null у ответа только с tool_calls
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaRequest struct {
	Model          string      `json:"model"`
	Messages       []oaMessage `json:"messages"`
	Tools          []oaTool    `json:"tools,omitempty"`
	ToolChoice     string      `json:"tool_choice,omitempty"`
	MaxTokens      int         `json:"max_tokens,omitempty"`
	MaxCompletion  int         `json:"max_completion_tokens,omitempty"`
	Reasoning      string      `json:"reasoning_effort,omitempty"`
	Temperature    *float64    `json:"temperature,omitempty"`
	ResponseFormat *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
}

type oaResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      oaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	// Некоторые провайдеры (OpenRouter) возвращают ошибку в теле со статусом 200.
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (c *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	body, err := json.Marshal(c.toWire(req))
	if err != nil {
		return Response{}, fmt.Errorf("%s: encode request: %w", c.name, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("%s: build request: %w", c.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("%s: %w", c.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Response{}, fmt.Errorf("%s: read response: %w", c.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg, code := parseError(raw)
		return Response{}, &APIError{
			Provider:   c.name,
			Status:     resp.StatusCode,
			Message:    msg,
			Code:       code,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
		}
	}

	var wire oaResponse
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Response{}, fmt.Errorf("%s: %w: %w", c.name, ErrBadResponse, err)
	}
	if wire.Error != nil {
		return Response{}, &APIError{Provider: c.name, Status: http.StatusBadGateway, Message: truncate(wire.Error.Message, 300)}
	}
	if len(wire.Choices) == 0 {
		return Response{}, fmt.Errorf("%s: %w: no choices", c.name, ErrBadResponse)
	}
	ch := wire.Choices[0]
	if ch.FinishReason == "length" && len(ch.Message.ToolCalls) == 0 {
		// Лимит токенов кончился до ответа (обычно ушёл на размышления): нужен reasoning_effort
		// или больший LLM_MAX_OUTPUT_TOKENS.
		return Response{}, fmt.Errorf("%s: %w", c.name, ErrOutputLimit)
	}
	msg := Message{Role: RoleAssistant}
	if ch.Message.Content != nil {
		msg.Content = *ch.Message.Content
	}
	for _, tc := range ch.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	model := wire.Model
	if model == "" {
		model = c.cfg.Model
	}
	return Response{
		Message:      msg,
		FinishReason: ch.FinishReason,
		Usage:        Usage{PromptTokens: wire.Usage.PromptTokens, CompletionTokens: wire.Usage.CompletionTokens},
		Model:        model,
		Provider:     c.name,
	}, nil
}

func (c *OpenAI) toWire(req Request) oaRequest {
	w := oaRequest{
		Model:       c.cfg.Model,
		ToolChoice:  string(req.ToolChoice),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	if w.MaxTokens == 0 {
		w.MaxTokens = c.cfg.MaxTokens
	}
	w.Reasoning = c.cfg.ReasoningEffort
	// OpenAI для reasoning-моделей (gpt-5, o-серия) принимает только max_completion_tokens
	// и температуру по умолчанию; остальные провайдеры ждут max_tokens.
	if c.openAIReasoning() {
		w.MaxCompletion, w.MaxTokens, w.Temperature = w.MaxTokens, 0, nil
	}
	if req.JSONMode {
		w.ResponseFormat = &struct {
			Type string `json:"type"`
		}{Type: "json_object"}
	}
	for _, m := range req.Messages {
		om := oaMessage{Role: string(m.Role), ToolCallID: m.ToolCallID}
		if m.Content != "" || len(m.ToolCalls) == 0 {
			content := m.Content
			om.Content = &content
		}
		for _, tc := range m.ToolCalls {
			var otc oaToolCall
			otc.ID, otc.Type = tc.ID, "function"
			otc.Function.Name, otc.Function.Arguments = tc.Name, tc.Arguments
			om.ToolCalls = append(om.ToolCalls, otc)
		}
		w.Messages = append(w.Messages, om)
	}
	for _, t := range req.Tools {
		var ot oaTool
		ot.Type = "function"
		ot.Function.Name, ot.Function.Description, ot.Function.Parameters = t.Name, t.Description, t.Parameters
		w.Tools = append(w.Tools, ot)
	}
	if len(w.Tools) == 0 {
		w.ToolChoice = "" // tool_choice без tools отвергается провайдерами
	}
	return w
}

func (c *OpenAI) openAIReasoning() bool {
	if !strings.Contains(c.cfg.BaseURL, "api.openai.com") {
		return false
	}
	m := c.cfg.Model
	return strings.HasPrefix(m, "gpt-5") || (len(m) > 1 && m[0] == 'o' && m[1] >= '0' && m[1] <= '9')
}

type wireError struct {
	Error struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
		Type    string `json:"type"`
	} `json:"error"`
}

// parseError достаёт текст и код ошибки из тела. Форматы: {"error":{...}} (OpenAI,
// OpenRouter) и [{"error":{...}}] (Gemini). Иначе - начало тела, обрезанное.
func parseError(raw []byte) (msg, code string) {
	var one wireError
	var many []wireError
	switch {
	case json.Unmarshal(raw, &one) == nil && one.Error.Message != "":
	case json.Unmarshal(raw, &many) == nil && len(many) > 0 && many[0].Error.Message != "":
		one = many[0]
	default:
		return truncate(strings.TrimSpace(string(raw)), 300), ""
	}
	code = one.Error.Type
	if s, ok := one.Error.Code.(string); ok && s != "" {
		code = s
	}
	return truncate(one.Error.Message, 300), code
}

// retryAfter разбирает Retry-After в секундах или как HTTP-дату.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
