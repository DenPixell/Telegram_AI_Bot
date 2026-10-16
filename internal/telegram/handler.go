package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"expense-bot/internal/agent"
	"expense-bot/internal/config"
	"expense-bot/internal/domain"
	"expense-bot/internal/onboarding"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/timezone"
)

// HandlerDeps - всё, что нужно обработчику апдейтов.
type HandlerDeps struct {
	Store       *postgres.Store
	Families    *service.Families
	Auth        *service.Auth
	Agent       *agent.Agent
	FSM         *onboarding.FSM
	Limiter     *pipeline.RateLimiter
	Sender      *Sender
	API         *API
	BotUsername func() string

	AllowedIDs []int64 // пусто - регистрация открыта всем
	AuthMode   string
	PublicURL  string
	InviteTTL  time.Duration
	AppTZ      string
}

// Handler маршрутизирует апдейт: онбординг -> служебные намерения -> агент.
// user_id всегда из апдейта Telegram, а не из текста.
type Handler struct {
	HandlerDeps
	mu     sync.Mutex
	warned map[int64]time.Time // когда пользователю последний раз писали о rate limit
}

func NewHandler(d HandlerDeps) *Handler {
	return &Handler{HandlerDeps: d, warned: map[int64]time.Time{}}
}

// Данные служебных кнопок.
const (
	cbLeave  = "leave:yes"
	cbDelete = "delete:yes"
	cbCancel = "cancel"
)

const (
	msgOnlyText   = "Я понимаю только текст. Напишите трату, например «кофе 350»."
	msgRateLimit  = "Слишком много сообщений подряд. Подождите минуту и продолжайте."
	msgClosed     = "Бот пока доступен только приглашённым пользователям."
	msgInternal   = "Что-то пошло не так, попробуйте ещё раз чуть позже."
	msgOwnerOnly  = "Это может только глава семьи."
	msgOwnerStays = "Вы глава семьи. Сначала передайте владение другому участнику в веб-панели (напишите «панель»)."
)

// HandleUpdate обрабатывает один апдейт. Ошибки инфраструктуры логируются, а пользователь
// получает шаблонный ответ.
func (h *Handler) HandleUpdate(ctx context.Context, up Update) {
	var err error
	var chatID int64
	switch {
	case up.CallbackQuery != nil:
		if m := up.CallbackQuery.Message; m != nil {
			chatID = m.Chat.ID
		}
		err = h.callback(ctx, up.CallbackQuery)
	case up.Message != nil:
		chatID = up.Message.Chat.ID
		err = h.message(ctx, up.Message)
	default:
		return
	}
	switch {
	case err == nil || errors.Is(err, context.Canceled):
	case IsBlocked(err):
		// Пользователь заблокировал бота: штатная ситуация, он уже помечен неактивным.
		slog.InfoContext(ctx, "user blocked the bot", "update_id", up.UpdateID)
	default:
		slog.ErrorContext(ctx, "update failed", "update_id", up.UpdateID, "err", err)
		if chatID != 0 {
			_ = h.Sender.Send(ctx, chatID, msgInternal, nil)
		}
	}
}

func (h *Handler) message(ctx context.Context, m *Message) error {
	if m.Chat.Type != "private" || m.From == nil || m.From.IsBot {
		return nil // группы и каналы не поддерживаются
	}
	from := *m.From
	if len(h.AllowedIDs) > 0 && !slices.Contains(h.AllowedIDs, from.ID) {
		return h.Sender.Send(ctx, m.Chat.ID, msgClosed, nil)
	}
	if !h.Limiter.Allow(from.ID, time.Now()) {
		if h.shouldWarn(from.ID) {
			return h.Sender.Send(ctx, m.Chat.ID, msgRateLimit, nil)
		}
		return nil
	}
	u, err := h.Store.UpsertUser(ctx, from.ID, from.DisplayName(), from.Username)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(m.Text)
	// Логируем длину, а не текст: сообщения - персональные данные.
	slog.DebugContext(ctx, "message", "user_id", u.TelegramID, "state", u.State, "len", len(text), "location", m.Location != nil)

	if cmd, payload, ok := command(text); ok && cmd == "start" {
		return h.reply(ctx, m.Chat.ID)(h.FSM.Start(ctx, u, payload))
	}
	if m.Location != nil {
		return h.reply(ctx, m.Chat.ID)(h.FSM.HandleLocation(ctx, u, m.Location.Latitude, m.Location.Longitude))
	}
	if text == "" {
		return h.Sender.Send(ctx, m.Chat.ID, msgOnlyText, nil)
	}

	var actor domain.Actor
	if u.State == domain.StateActive {
		actor, err = h.Families.Actor(ctx, u.TelegramID)
		if errors.Is(err, domain.ErrNotInFamily) {
			// Пользователя удалили из семьи: снова выбор семьи.
			if err := h.Store.SetUserState(ctx, u.TelegramID, domain.StateAwaitingChoice, nil); err != nil {
				return err
			}
			u.State = domain.StateAwaitingChoice
		} else if err != nil {
			return err
		}
	}
	if u.State != domain.StateActive {
		return h.reply(ctx, m.Chat.ID)(h.FSM.HandleText(ctx, u, text))
	}

	if in, ok := pipeline.MatchIntent(text); ok {
		return h.intent(ctx, m.Chat.ID, u, actor, in)
	}
	r, err := h.Agent.Handle(ctx, actor, text)
	if err != nil {
		return err
	}
	return h.Sender.Send(ctx, m.Chat.ID, r.Text, nil)
}

func (h *Handler) intent(ctx context.Context, chatID int64, u domain.User, a domain.Actor, in pipeline.Intent) error {
	send := func(text string, markup *ReplyMarkup) error { return h.Sender.Send(ctx, chatID, text, markup) }
	switch in.Kind {
	case pipeline.IntentHelp:
		return send(onboarding.Help(a.IsOwner()), nil)

	case pipeline.IntentInvite:
		inv, err := h.Families.CreateInvite(ctx, a)
		if errors.Is(err, domain.ErrForbidden) {
			return send(msgOwnerOnly+" Попросите у неё или него ссылку-приглашение.", nil)
		}
		if err != nil {
			return err
		}
		return send(onboarding.InviteText(h.BotUsername(), inv, h.InviteTTL)+"\nОтправьте ссылку или код тому, кого хотите пригласить.", nil)

	case pipeline.IntentPanel:
		if !a.IsOwner() {
			return send("Веб-панель доступна главе семьи. Свои траты можно смотреть здесь: «покажи траты за месяц».", nil)
		}
		base := strings.TrimRight(h.PublicURL, "/")
		if h.AuthMode == config.PanelAuthNone {
			return send("Веб-панель: "+base, nil)
		}
		token, _, err := h.Auth.CreateLoginToken(ctx, a)
		if err != nil {
			return err
		}
		return send(fmt.Sprintf("Ссылка для входа в панель (одноразовая, действует %d минут):\n%s/auth?token=%s\n\nНикому её не пересылайте.",
			int(service.LoginTokenTTL.Minutes()), base, token), nil)

	case pipeline.IntentTimezone:
		if in.Arg == "" {
			fam, err := h.Families.GetFamily(ctx, a)
			if err != nil {
				return err
			}
			loc := timezone.Resolve(u.Timezone, fam.DefaultTZ, h.AppTZ)
			return send(fmt.Sprintf("Ваш часовой пояс: %s (%s). Сменить: «мой часовой пояс Новосибирск» или «мой часовой пояс UTC+5».",
				loc, timezone.OffsetLabel(loc, time.Now())), nil)
		}
		z, err := timezone.Parse(in.Arg)
		if err != nil {
			return send("Не узнал такой часовой пояс. Напишите город или смещение, например «мой часовой пояс Екатеринбург» или «UTC+5».", nil)
		}
		if err := h.Store.SetUserTimezone(ctx, u.TelegramID, z.Name); err != nil {
			return err
		}
		return send("Часовой пояс: "+z.Describe(time.Now())+". Новые траты будут записываться по нему.", nil)

	case pipeline.IntentLeave:
		n, err := h.Families.MemberCount(ctx, a)
		if err != nil {
			return err
		}
		text := "Выйти из семьи? Ваши траты останутся в семье."
		switch {
		case a.IsOwner() && n > 1:
			return send(msgOwnerStays, nil)
		case a.IsOwner():
			text = "Вы единственный участник: семья и все её траты будут удалены. Выйти?"
		}
		return send(text, confirm("Да, выйти", cbLeave))

	case pipeline.IntentDeleteData:
		if n, err := h.Families.MemberCount(ctx, a); err != nil {
			return err
		} else if a.IsOwner() && n > 1 {
			return send(msgOwnerStays, nil)
		}
		return send("Удалить все ваши данные: траты, участие в семье и профиль? Это необратимо.", confirm("Да, удалить", cbDelete))
	}
	return nil
}

func (h *Handler) callback(ctx context.Context, cq *CallbackQuery) error {
	_ = h.API.AnswerCallback(ctx, cq.ID, "")
	if cq.Message == nil || cq.Message.Chat.Type != "private" {
		return nil
	}
	chatID := cq.Message.Chat.ID
	// Кнопки одноразовые: убираем их, чтобы повторное нажатие ничего не делало.
	_ = h.API.RemoveInlineKeyboard(ctx, chatID, cq.Message.MessageID)
	if len(h.AllowedIDs) > 0 && !slices.Contains(h.AllowedIDs, cq.From.ID) {
		return nil
	}
	u, err := h.Store.UpsertUser(ctx, cq.From.ID, cq.From.DisplayName(), cq.From.Username)
	if err != nil {
		return err
	}
	switch cq.Data {
	case cbCancel:
		return h.Sender.Send(ctx, chatID, "Хорошо, ничего не меняю.", nil)
	case cbLeave:
		a, err := h.Families.Actor(ctx, u.TelegramID)
		if errors.Is(err, domain.ErrNotInFamily) {
			return h.sendReply(ctx, chatID, onboarding.Welcome())
		}
		if err != nil {
			return err
		}
		deleted, err := h.Families.Leave(ctx, a)
		if errors.Is(err, domain.ErrOwnerMustStay) {
			return h.Sender.Send(ctx, chatID, msgOwnerStays, nil)
		}
		if err != nil {
			return err
		}
		text := "Вы вышли из семьи."
		if deleted {
			text = "Вы вышли из семьи, семья удалена."
		}
		if err := h.Sender.Send(ctx, chatID, text, nil); err != nil {
			return err
		}
		return h.sendReply(ctx, chatID, onboarding.Welcome())
	case cbDelete:
		_, err := h.Families.DeleteMyData(ctx, u.TelegramID)
		if errors.Is(err, domain.ErrOwnerMustStay) {
			return h.Sender.Send(ctx, chatID, msgOwnerStays, nil)
		}
		if err != nil {
			return err
		}
		return h.Sender.Send(ctx, chatID, "Ваши данные удалены. Если захотите вернуться — напишите /start.", &ReplyMarkup{RemoveKeyboard: true})
	}
	if strings.HasPrefix(cq.Data, "onb:") {
		return h.reply(ctx, chatID)(h.FSM.Callback(ctx, u, cq.Data))
	}
	return nil
}

// reply отправляет ответ онбординга, переводя его кнопки в клавиатуры Telegram.
// Каррирование позволяет писать h.reply(ctx, chat)(h.FSM.Start(...)).
func (h *Handler) reply(ctx context.Context, chatID int64) func(onboarding.Reply, error) error {
	return func(r onboarding.Reply, err error) error {
		if err != nil {
			return err
		}
		return h.sendReply(ctx, chatID, r)
	}
}

func (h *Handler) sendReply(ctx context.Context, chatID int64, r onboarding.Reply) error {
	var markup *ReplyMarkup
	switch {
	case len(r.Buttons) > 0:
		markup = &ReplyMarkup{}
		for _, row := range r.Buttons {
			var line []InlineButton
			for _, b := range row {
				line = append(line, InlineButton{Text: b.Text, CallbackData: b.Data})
			}
			markup.InlineKeyboard = append(markup.InlineKeyboard, line)
		}
	case r.RequestLocation:
		markup = &ReplyMarkup{
			Keyboard:       [][]KeyboardButton{{{Text: "📍 Отправить геолокацию", RequestLocation: true}}, {{Text: "Пропустить"}}},
			ResizeKeyboard: true, OneTimeKeyboard: true,
		}
	case r.RemoveKeyboard:
		markup = &ReplyMarkup{RemoveKeyboard: true}
	}
	return h.Sender.Send(ctx, chatID, r.Text, markup)
}

func confirm(yes, data string) *ReplyMarkup {
	return &ReplyMarkup{InlineKeyboard: [][]InlineButton{{{Text: yes, CallbackData: data}, {Text: "Отмена", CallbackData: cbCancel}}}}
}

// shouldWarn - писать о rate limit не чаще раза в минуту, иначе предупреждения сами станут спамом.
func (h *Handler) shouldWarn(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if t, ok := h.warned[userID]; ok && now.Sub(t) < time.Minute {
		return false
	}
	if len(h.warned) > 10_000 {
		clear(h.warned)
	}
	h.warned[userID] = now
	return true
}

// command разбирает «/start CODE» и «/start@bot CODE».
func command(text string) (name, payload string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest), true
}
