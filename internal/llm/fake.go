package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Fake - скриптованный клиент для тестов агента и хаос-тестов: без сети и недетерминизма.
// Ответы выдаются по очереди из Script; если очередь пуста - вызывается Handler, если он
// задан, иначе ошибка. Все запросы сохраняются в Calls, чтобы проверять, например,
// что на отказных сообщениях LLM не вызывалась вовсе.
type Fake struct {
	Handler func(ctx context.Context, req Request) (Response, error)

	mu     sync.Mutex
	script []fakeStep
	calls  []Request
}

type fakeStep struct {
	resp Response
	err  error
}

var errFakeExhausted = errors.New("fake llm: no scripted response left")

func NewFake() *Fake { return &Fake{} }

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Complete(ctx context.Context, req Request) (Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	var step *fakeStep
	if len(f.script) > 0 {
		s := f.script[0]
		f.script = f.script[1:]
		step = &s
	}
	h := f.Handler
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	switch {
	case step != nil:
		if step.err == nil && step.resp.Provider == "" {
			step.resp.Provider = "fake"
		}
		return step.resp, step.err
	case h != nil:
		return h(ctx, req)
	}
	return Response{}, errFakeExhausted
}

// ReplyTools добавляет в очередь ответ с вызовами инструментов. args маршалится в JSON.
func (f *Fake) ReplyTools(calls ...FakeCall) *Fake {
	msg := Message{Role: RoleAssistant}
	for i, c := range calls {
		args := c.RawArgs
		if args == "" {
			b, err := json.Marshal(c.Args)
			if err != nil {
				panic(fmt.Sprintf("fake llm: marshal args: %v", err))
			}
			args = string(b)
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: fmt.Sprintf("call_%d", i+1), Name: c.Name, Arguments: args})
	}
	return f.push(fakeStep{resp: Response{Message: msg, FinishReason: "tool_calls", Model: "fake", Usage: Usage{PromptTokens: 100, CompletionTokens: 10}}})
}

// ReplyText добавляет текстовый ответ (AGENT_MODE=json или модель нарушила tool_choice).
func (f *Fake) ReplyText(text string) *Fake {
	return f.push(fakeStep{resp: Response{Message: Message{Role: RoleAssistant, Content: text}, FinishReason: "stop", Model: "fake"}})
}

// ReplyError добавляет ошибку: &APIError{Status: 429}, context.DeadlineExceeded и т.д.
func (f *Fake) ReplyError(err error) *Fake { return f.push(fakeStep{err: err}) }

func (f *Fake) push(s fakeStep) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = append(f.script, s)
	return f
}

// Calls - копия всех полученных запросов.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.calls...)
}

func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// FakeCall - вызов инструмента в скриптованном ответе. RawArgs позволяет подсунуть
// невалидный JSON для хаос-тестов.
type FakeCall struct {
	Name    string
	Args    any
	RawArgs string
}
