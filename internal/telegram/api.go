package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Минимальный клиент Bot API на стандартной библиотеке: нужны пять методов, а повтор по
// retry_after, лимитер и дедупликация всё равно свои.

const DefaultAPIURL = "https://api.telegram.org"

type API struct {
	base   string // https://api.telegram.org/bot<token>
	client *http.Client
}

// NewAPI: apiURL - TELEGRAM_API_URL или DefaultAPIURL. Прокси - из HTTPS_PROXY.
func NewAPI(apiURL, token string) *API {
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	return &API{
		base: strings.TrimRight(apiURL, "/") + "/bot" + token,
		// Таймаут больше long polling (50 с), чтобы getUpdates не обрывался.
		client: &http.Client{Timeout: 70 * time.Second},
	}
}

// APIError - ответ Bot API с ok=false. Описание не содержит токен.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// IsBlocked - пользователь заблокировал бота или удалил аккаунт.
func IsBlocked(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == http.StatusForbidden
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (a *API) call(ctx context.Context, method string, params, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("telegram %s: encode: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		// Ошибка net/http содержит URL с токеном - не пропускаем её текст дальше.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("telegram %s: request failed: %w", method, redact(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("telegram %s: read: %w", method, err)
	}
	var r apiResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("telegram %s: http %d: bad response", method, resp.StatusCode)
	}
	if !r.OK {
		return &APIError{Method: method, Code: r.ErrorCode, Description: r.Description,
			RetryAfter: time.Duration(r.Parameters.RetryAfter) * time.Second}
	}
	if result != nil {
		if err := json.Unmarshal(r.Result, result); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

// redact убирает URL (с токеном) из сетевой ошибки.
func redact(err error) error {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) {
		if inner := ue.Unwrap(); inner != nil {
			return inner
		}
	}
	return errors.New("network error")
}

// ---------- типы ----------

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

func (u User) DisplayName() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // private | group | supergroup | channel
}

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type Message struct {
	MessageID int64     `json:"message_id"`
	From      *User     `json:"from"`
	Chat      Chat      `json:"chat"`
	Text      string    `json:"text"`
	Location  *Location `json:"location"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type KeyboardButton struct {
	Text            string `json:"text"`
	RequestLocation bool   `json:"request_location,omitempty"`
}

// ReplyMarkup - одна из клавиатур Telegram; заполняется только нужное поле.
type ReplyMarkup struct {
	InlineKeyboard  [][]InlineButton   `json:"inline_keyboard,omitempty"`
	Keyboard        [][]KeyboardButton `json:"keyboard,omitempty"`
	ResizeKeyboard  bool               `json:"resize_keyboard,omitempty"`
	OneTimeKeyboard bool               `json:"one_time_keyboard,omitempty"`
	RemoveKeyboard  bool               `json:"remove_keyboard,omitempty"`
}

// ---------- методы ----------

func (a *API) GetMe(ctx context.Context) (User, error) {
	var u User
	err := a.call(ctx, "getMe", struct{}{}, &u)
	return u, err
}

// GetUpdates - long polling. Обновления до offset считаются подтверждёнными.
func (a *API) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	var ups []Update
	err := a.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}, &ups)
	return ups, err
}

type sendParams struct {
	ChatID      int64        `json:"chat_id"`
	Text        string       `json:"text"`
	ReplyMarkup *ReplyMarkup `json:"reply_markup,omitempty"`
	// Превью ссылок выключены: ссылки входа и приглашения не должны раскрываться.
	LinkPreview struct {
		IsDisabled bool `json:"is_disabled"`
	} `json:"link_preview_options"`
}

// SendMessage отправляет обычный текст без разметки: данные пользователей не попадают
// в Markdown/HTML.
func (a *API) SendMessage(ctx context.Context, chatID int64, text string, markup *ReplyMarkup) error {
	p := sendParams{ChatID: chatID, Text: text, ReplyMarkup: markup}
	p.LinkPreview.IsDisabled = true
	return a.call(ctx, "sendMessage", p, nil)
}

func (a *API) AnswerCallback(ctx context.Context, id, text string) error {
	return a.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// RemoveInlineKeyboard убирает кнопки у сообщения после нажатия.
func (a *API) RemoveInlineKeyboard(ctx context.Context, chatID, messageID int64) error {
	return a.call(ctx, "editMessageReplyMarkup", map[string]any{
		"chat_id": chatID, "message_id": messageID, "reply_markup": map[string]any{"inline_keyboard": [][]InlineButton{}},
	}, nil)
}

type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func (a *API) SetMyCommands(ctx context.Context, cmds []Command) error {
	return a.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}
