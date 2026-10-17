package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const okReply = `{"model":"m-ok","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"add_expense","arguments":"{\"amount\":\"350\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1200,"completion_tokens":25}}`

// mockProvider - OpenAI-совместимый сервер: отвечает по очереди из responses, затем последним.
type mockProvider struct {
	t         *testing.T
	mu        sync.Mutex
	responses []func(w http.ResponseWriter)
	hits      atomic.Int32
	lastBody  map[string]any
	lastAuth  string
}

func newMock(t *testing.T, rs ...func(w http.ResponseWriter)) (*mockProvider, *httptest.Server) {
	m := &mockProvider{t: t, responses: rs}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		n := int(m.hits.Add(1)) - 1
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		_ = json.Unmarshal(b, &m.lastBody)
		m.lastAuth = r.Header.Get("Authorization")
		m.mu.Unlock()
		m.responses[min(n, len(m.responses)-1)](w)
	}))
	t.Cleanup(srv.Close)
	return m, srv
}

func ok(w http.ResponseWriter) { _, _ = io.WriteString(w, okReply) }

func status(code int, body string, headers ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

func client(srv *httptest.Server, cfg ...func(*OpenAIConfig)) *OpenAI {
	c := OpenAIConfig{BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "m", Timeout: 2 * time.Second, MaxTokens: 200}
	for _, f := range cfg {
		f(&c)
	}
	return NewOpenAI(c)
}

var testReq = Request{
	Messages:   []Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "кофе 350"}},
	Tools:      []Tool{{Name: "add_expense", Parameters: json.RawMessage(`{"type":"object"}`)}},
	ToolChoice: ToolChoiceRequired,
}

func fastRetry() RetryConfig {
	return RetryConfig{Attempts: 2, BaseDelay: time.Millisecond, MaxDelay: 1500 * time.Millisecond, MaxRetryAfter: 2 * time.Second}
}

func TestOpenAIToolCallAndWireFormat(t *testing.T) {
	m, srv := newMock(t, ok)
	resp, err := client(srv).Complete(context.Background(), testReq)
	require.NoError(t, err)
	require.Len(t, resp.Message.ToolCalls, 1)
	assert.Equal(t, "add_expense", resp.Message.ToolCalls[0].Name)
	assert.JSONEq(t, `{"amount":"350"}`, resp.Message.ToolCalls[0].Arguments)
	assert.Equal(t, Usage{PromptTokens: 1200, CompletionTokens: 25}, resp.Usage)
	assert.Equal(t, "m-ok", resp.Model)

	assert.Equal(t, "Bearer sk-test", m.lastAuth)
	assert.Equal(t, "required", m.lastBody["tool_choice"])
	assert.EqualValues(t, 200, m.lastBody["max_tokens"], "MaxTokens клиента по умолчанию")
	assert.NotContains(t, m.lastBody, "reasoning_effort", "без настройки не передаётся")
}

func TestOpenAIReasoningEffortAndOpenAIQuirks(t *testing.T) {
	m, srv := newMock(t, ok)
	_, err := client(srv, func(c *OpenAIConfig) { c.ReasoningEffort = "none" }).Complete(context.Background(), testReq)
	require.NoError(t, err)
	assert.Equal(t, "none", m.lastBody["reasoning_effort"])

	// Для gpt-5 на api.openai.com - max_completion_tokens и без temperature.
	c := NewOpenAI(OpenAIConfig{BaseURL: "https://api.openai.com/v1", Model: "gpt-5-mini", MaxTokens: 200})
	w := c.toWire(Request{Temperature: Float(0)})
	assert.Zero(t, w.MaxTokens)
	assert.Equal(t, 200, w.MaxCompletion)
	assert.Nil(t, w.Temperature)
}

func TestOpenAIToolResultRoundTrip(t *testing.T) {
	m, srv := newMock(t, ok)
	req := testReq
	req.Messages = append(req.Messages,
		Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "add_expense", Arguments: `{}`}}},
		Message{Role: RoleTool, ToolCallID: "c1", Content: `{"ok":true}`})
	_, err := client(srv).Complete(context.Background(), req)
	require.NoError(t, err)
	msgs := m.lastBody["messages"].([]any)
	assistant := msgs[2].(map[string]any)
	assert.Nil(t, assistant["content"], "у ответа только с tool_calls content=null")
	assert.Equal(t, "c1", msgs[3].(map[string]any)["tool_call_id"])
}

func TestOpenAIErrors(t *testing.T) {
	cases := []struct {
		name      string
		resp      func(w http.ResponseWriter)
		retryable bool
		check     func(t *testing.T, err error)
	}{
		{"400 is request error", status(400, `{"error":{"message":"bad tools"}}`), false, func(t *testing.T, err error) {
			var e *APIError
			require.ErrorAs(t, err, &e)
			assert.Equal(t, "bad tools", e.Message)
		}},
		{"gemini array error body", status(503, `[{"error":{"code":503,"message":"high demand","status":"UNAVAILABLE"}}]`), true, func(t *testing.T, err error) {
			assert.Contains(t, err.Error(), "high demand")
		}},
		{"429 with Retry-After", status(429, `{"error":{"message":"slow down"}}`, "Retry-After", "3"), true, func(t *testing.T, err error) {
			var e *APIError
			require.ErrorAs(t, err, &e)
			assert.Equal(t, 3*time.Second, e.RetryAfter)
		}},
		{"429 no credits is not retried", status(429, `{"error":{"message":"You have no credits remaining","code":"insufficient_quota"}}`), false, func(t *testing.T, err error) {
			var e *APIError
			require.ErrorAs(t, err, &e)
			assert.True(t, e.ProviderFault())
		}},
		{"200 with error body", status(200, `{"error":{"message":"provider returned error"}}`), true, nil},
		{"garbage", status(200, `<html>oops</html>`), true, func(t *testing.T, err error) { assert.ErrorIs(t, err, ErrBadResponse) }},
		{"no choices", status(200, `{"choices":[]}`), true, func(t *testing.T, err error) { assert.ErrorIs(t, err, ErrBadResponse) }},
		{"token limit spent on thinking", status(200, `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"length"}]}`), false,
			func(t *testing.T, err error) { assert.ErrorIs(t, err, ErrOutputLimit) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, srv := newMock(t, c.resp)
			_, err := client(srv).Complete(context.Background(), testReq)
			require.Error(t, err)
			assert.Equal(t, c.retryable, IsRetryable(err))
			if c.check != nil {
				c.check(t, err)
			}
		})
	}
}

func TestTimeoutIsRetryable(t *testing.T) {
	_, srv := newMock(t, func(w http.ResponseWriter) { time.Sleep(300 * time.Millisecond); ok(w) })
	_, err := client(srv, func(c *OpenAIConfig) { c.Timeout = 50 * time.Millisecond }).Complete(context.Background(), testReq)
	require.Error(t, err)
	assert.True(t, IsRetryable(err))
}

func TestRetry(t *testing.T) {
	t.Run("retries transient then succeeds", func(t *testing.T) {
		m, srv := newMock(t, status(503, "down"), ok)
		_, err := WithRetry(client(srv), fastRetry()).Complete(context.Background(), testReq)
		require.NoError(t, err)
		assert.EqualValues(t, 2, m.hits.Load())
	})
	t.Run("honours short Retry-After", func(t *testing.T) {
		m, srv := newMock(t, status(429, "", "Retry-After", "1"), ok)
		start := time.Now()
		_, err := WithRetry(client(srv), fastRetry()).Complete(context.Background(), testReq)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond)
		assert.EqualValues(t, 2, m.hits.Load())
	})
	t.Run("long Retry-After goes to fallback instead of waiting", func(t *testing.T) {
		m, srv := newMock(t, status(429, "", "Retry-After", "60"), ok)
		_, err := WithRetry(client(srv), fastRetry()).Complete(context.Background(), testReq)
		require.Error(t, err)
		assert.EqualValues(t, 1, m.hits.Load())
	})
	t.Run("does not retry request errors", func(t *testing.T) {
		m, srv := newMock(t, status(400, "bad"), ok)
		_, err := WithRetry(client(srv), fastRetry()).Complete(context.Background(), testReq)
		require.Error(t, err)
		assert.EqualValues(t, 1, m.hits.Load())
	})
	t.Run("canceled context is not retried", func(t *testing.T) {
		m, srv := newMock(t, ok)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := WithRetry(client(srv), fastRetry()).Complete(ctx, testReq)
		require.Error(t, err)
		assert.False(t, IsRetryable(err))
		assert.Zero(t, m.hits.Load())
	})
}

func TestBreaker(t *testing.T) {
	now := time.Unix(0, 0)
	m, srv := newMock(t, status(503, "down"))
	b := WithBreaker(client(srv), BreakerConfig{Failures: 3, Cooldown: time.Minute}).(*breaker)
	b.now = func() time.Time { return now }

	for range 3 {
		_, err := b.Complete(context.Background(), testReq)
		assert.Error(t, err)
	}
	_, err := b.Complete(context.Background(), testReq)
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.EqualValues(t, 3, m.hits.Load(), "разомкнутый breaker не шлёт запросы")

	// После паузы - один пробный запрос; неудача снова размыкает.
	now = now.Add(61 * time.Second)
	_, err = b.Complete(context.Background(), testReq)
	assert.NotErrorIs(t, err, ErrCircuitOpen)
	_, err = b.Complete(context.Background(), testReq)
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.EqualValues(t, 4, m.hits.Load())
}

func TestBreakerIgnoresRequestErrorsButNotDeadProvider(t *testing.T) {
	_, bad := newMock(t, status(400, "bad request"))
	b := WithBreaker(client(bad), BreakerConfig{Failures: 2, Cooldown: time.Minute})
	for range 5 {
		_, err := b.Complete(context.Background(), testReq)
		assert.NotErrorIs(t, err, ErrCircuitOpen, "400 - ошибка запроса, не провайдера")
	}

	_, noKey := newMock(t, status(401, "invalid api key"))
	b = WithBreaker(client(noKey), BreakerConfig{Failures: 2, Cooldown: time.Minute})
	_, _ = b.Complete(context.Background(), testReq)
	_, _ = b.Complete(context.Background(), testReq)
	_, err := b.Complete(context.Background(), testReq)
	assert.ErrorIs(t, err, ErrCircuitOpen, "неверный ключ исключает провайдера")
}

func TestFallback(t *testing.T) {
	_, down := newMock(t, status(503, "down"))
	_, up := newMock(t, ok)
	resp, err := WithFallback(client(down), client(up)).Complete(context.Background(), testReq)
	require.NoError(t, err)
	assert.Contains(t, resp.Provider, "127.0.0.1")

	_, bad := newMock(t, status(400, "bad"))
	_, err = WithFallback(client(down), client(bad)).Complete(context.Background(), testReq)
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "down")
	assert.Contains(t, err.Error(), "bad", "причины всех провайдеров в ошибке")
}

func TestConcurrencyLimit(t *testing.T) {
	_, slow := newMock(t, func(w http.ResponseWriter) { time.Sleep(300 * time.Millisecond); ok(w) })
	c := WithConcurrency(client(slow), 1, 50*time.Millisecond)
	errs := make(chan error, 2)
	go func() { _, err := c.Complete(context.Background(), testReq); errs <- err }()
	time.Sleep(20 * time.Millisecond)
	_, err := c.Complete(context.Background(), testReq)
	assert.ErrorIs(t, err, ErrBusy)
	assert.NoError(t, <-errs)
}

type memRecorder struct {
	mu   sync.Mutex
	recs []UsageRecord
}

func (m *memRecorder) RecordUsage(_ context.Context, r UsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, r)
	return nil
}

func TestUsageCountsEveryRealAttempt(t *testing.T) {
	_, srv := newMock(t, status(503, "down"), ok)
	rec := &memRecorder{}
	c := WithBreaker(WithRetry(WithUsage(client(srv), rec, "m"), fastRetry()), DefaultBreaker)
	ctx := WithCaller(context.Background(), Caller{UserID: 7_000_000_001, FamilyID: 5})
	_, err := c.Complete(ctx, testReq)
	require.NoError(t, err)
	require.Len(t, rec.recs, 2, "повтор - это тоже запрос к провайдеру")
	assert.False(t, rec.recs[0].OK)
	assert.True(t, rec.recs[1].OK)
	assert.Equal(t, 1200, rec.recs[1].PromptTokens)
	assert.Equal(t, int64(7_000_000_001), rec.recs[1].Caller.UserID)
}

func TestFake(t *testing.T) {
	f := NewFake().
		ReplyTools(FakeCall{Name: "add_expense", Args: map[string]any{"amount": "350"}}).
		ReplyError(&APIError{Status: 429}).
		ReplyTools(FakeCall{Name: "x", RawArgs: "{broken"})
	r, err := f.Complete(context.Background(), testReq)
	require.NoError(t, err)
	assert.JSONEq(t, `{"amount":"350"}`, r.Message.ToolCalls[0].Arguments)
	_, err = f.Complete(context.Background(), testReq)
	assert.Error(t, err)
	r, _ = f.Complete(context.Background(), testReq)
	assert.Equal(t, "{broken", r.Message.ToolCalls[0].Arguments)
	_, err = f.Complete(context.Background(), testReq)
	assert.Error(t, err, "очередь пуста")
	assert.Equal(t, 4, f.CallCount())
	assert.True(t, strings.Contains(f.Calls()[0].Messages[1].Content, "кофе"))
}
