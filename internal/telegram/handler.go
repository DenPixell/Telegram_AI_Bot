package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"expense-bot/internal/agent"
	"expense-bot/internal/config"
	"expense-bot/internal/domain"
	"expense-bot/internal/metrics"
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
	AdminIDs   []int64 // могут делать рассылку /broadcast
	Broadcast  *Broadcaster
	AuthMode   string
	PublicURL  string
	InviteTTL  time.Duration
	AppTZ      string
}

// Handler маршрутизирует апдейт: онбординг -> служебные намерения -> агент.
// user_id всегда из апдейта Telegram, а не из текста.
type Handler struct {
	HandlerDeps
	mu       sync.Mutex
	warned   map[int64]time.Time // когда пользователю последний раз писали о rate limit
	confirms map[int64]pendingConfirm
}

func NewHandler(d HandlerDeps) *Handler {
	return &Handler{HandlerDeps: d, warned: map[int64]time.Time{}, confirms: map[int64]pendingConfirm{}}
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
		metrics.TelegramUpdates.Inc("processed")
	case IsBlocked(err):
		metrics.TelegramUpdates.Inc("processed")
		// Пользователь заблокировал бота: штатная ситуация, он уже помечен неактивным.
		slog.InfoContext(ctx, "user blocked the bot", "update_id", up.UpdateID)
	default:
		metrics.TelegramUpdates.Inc("failed")
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

	if cmd, payload, ok := command(text); ok && cmd == "broadcast" && h.isAdmin(from.ID) {
		return h.broadcastDraft(ctx, m.Chat.ID, from.ID, payload)
	}
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
		// «кофе 350» первым сообщением: запоминаем и запишем после регистрации, а не теряем.
		if h.rememberable(ctx, u, text) {
			return h.reply(ctx, m.Chat.ID)(h.FSM.Remember(ctx, u, text))
		}
		return h.reply(ctx, m.Chat.ID)(h.FSM.HandleText(ctx, u, text))
	}

	if handled, err := h.confirmByText(ctx, m.Chat.ID, u, text); handled || err != nil {
		return err
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
		h.askConfirm(u.TelegramID, cbLeave)
		return send(text+" Ответьте «да» или «нет».", confirm("Да, выйти", cbLeave))

	case pipeline.IntentDeleteData:
		if n, err := h.Families.MemberCount(ctx, a); err != nil {
			return err
		} else if a.IsOwner() && n > 1 {
			return send(msgOwnerStays, nil)
		}
		h.askConfirm(u.TelegramID, cbDelete)
		return send("Удалить все ваши данные: траты, участие в семье и профиль? Это необратимо. Ответьте «да» или «нет».", confirm("Да, удалить", cbDelete))
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
		h.takeConfirm(u.TelegramID)
		return h.Sender.Send(ctx, chatID, "Хорошо, ничего не меняю.", nil)
	case cbLeave:
		h.takeConfirm(u.TelegramID) // нажата кнопка - текстовое «да» больше не ждём
		return h.doLeave(ctx, chatID, u)
	case cbDelete:
		h.takeConfirm(u.TelegramID)
		return h.doDelete(ctx, chatID, u)
	}
	if strings.HasPrefix(cq.Data, "bc:") && h.isAdmin(cq.From.ID) {
		return h.broadcastCallback(ctx, chatID, cq.Data)
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
		if err := h.sendReply(ctx, chatID, r); err != nil {
			return err
		}
		if r.Pending != "" {
			return h.recordPending(ctx, chatID, r.Pending)
		}
		return nil
	}
}

// rememberable: пользователь ещё выбирает семью и прислал трату, а не код приглашения.
func (h *Handler) rememberable(ctx context.Context, u domain.User, text string) bool {
	if u.State != domain.StateNew && u.State != domain.StateAwaitingChoice {
		return false
	}
	if _, isCode := service.NormalizeInviteCode(text); isCode {
		return false
	}
	return h.Agent != nil && h.Agent.LooksLikeExpense(ctx, u.TelegramID, text)
}

// recordPending записывает трату, отложенную до конца регистрации.
func (h *Handler) recordPending(ctx context.Context, chatID int64, text string) error {
	actor, err := h.Families.Actor(ctx, chatID)
	if err != nil {
		return err
	}
	r, err := h.Agent.Handle(ctx, actor, text)
	if err != nil {
		return err
	}
	return h.Sender.Send(ctx, chatID, r.Text, nil)
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

func (h *Handler) isAdmin(userID int64) bool {
	return h.Broadcast != nil && slices.Contains(h.AdminIDs, userID)
}

// broadcastDraft - первый шаг рассылки: черновик и предпросмотр с подтверждением.
func (h *Handler) broadcastDraft(ctx context.Context, chatID, adminID int64, text string) error {
	bc, n, err := h.Broadcast.Draft(ctx, text, adminID)
	if errors.Is(err, ErrEmptyBroadcast) {
		return h.Sender.Send(ctx, chatID, "Напишите текст после команды, до 4000 символов. Например:\n"+
			"/broadcast Сегодня с 22:00 до 23:00 бот на техобслуживании. Траты, отправленные в это время, запишутся после.", nil)
	}
	if err != nil {
		return err
	}
	preview := fmt.Sprintf("Рассылку №%d получат %s. Так они увидят сообщение:\n\n%s", bc.ID, plural(n), bc.Text)
	id := strconv.FormatInt(bc.ID, 10)
	return h.Sender.Send(ctx, chatID, preview, &ReplyMarkup{InlineKeyboard: [][]InlineButton{{
		{Text: "📢 Отправить", CallbackData: "bc:send:" + id},
		{Text: "Отмена", CallbackData: "bc:cancel:" + id},
	}}})
}

func (h *Handler) broadcastCallback(ctx context.Context, chatID int64, data string) error {
	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		return nil
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return nil
	}
	switch parts[1] {
	case "cancel":
		if err := h.Broadcast.Cancel(ctx, id); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return h.Sender.Send(ctx, chatID, "Рассылка отменена.", nil)
	case "send":
		bc, err := h.Broadcast.Start(ctx, id)
		switch {
		case errors.Is(err, domain.ErrConflict):
			return h.Sender.Send(ctx, chatID, "Сейчас уже идёт другая рассылка. Дождитесь отчёта и попробуйте снова.", nil)
		case errors.Is(err, domain.ErrNotFound):
			return h.Sender.Send(ctx, chatID, "Эта рассылка уже отправлена или отменена.", nil)
		case err != nil:
			return err
		}
		return h.Sender.Send(ctx, chatID, fmt.Sprintf("Рассылка №%d запущена: %s. Пришлю отчёт, когда закончу.", bc.ID, plural(bc.Total)), nil)
	}
	return nil
}

// plural: 1 пользователь, 2 пользователя, 5 пользователей.
func plural(n int) string {
	m10, m100 := n%10, n%100
	switch {
	case m10 == 1 && m100 != 11:
		return fmt.Sprintf("%d пользователь", n)
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		return fmt.Sprintf("%d пользователя", n)
	}
	return fmt.Sprintf("%d пользователей", n)
}

// doLeave - выход из семьи после подтверждения (кнопкой или словом «да»).
func (h *Handler) doLeave(ctx context.Context, chatID int64, u domain.User) error {
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
}

// doDelete - удаление всех данных пользователя после подтверждения.
func (h *Handler) doDelete(ctx context.Context, chatID int64, u domain.User) error {
	_, err := h.Families.DeleteMyData(ctx, u.TelegramID)
	if errors.Is(err, domain.ErrOwnerMustStay) {
		return h.Sender.Send(ctx, chatID, msgOwnerStays, nil)
	}
	if err != nil {
		return err
	}
	return h.Sender.Send(ctx, chatID, "Ваши данные удалены. Если захотите вернуться — напишите /start.", &ReplyMarkup{RemoveKeyboard: true})
}

// Подтверждение текстом: «без кнопок» должно работать и для выхода из семьи,
// и для удаления данных. Ожидание живёт 5 минут и сбрасывается любым другим сообщением.
const confirmTTL = 5 * time.Minute

type pendingConfirm struct {
	action string // cbLeave | cbDelete
	at     time.Time
}

func (h *Handler) askConfirm(userID int64, action string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.confirms[userID] = pendingConfirm{action: action, at: time.Now()}
}

func (h *Handler) takeConfirm(userID int64) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.confirms[userID]
	delete(h.confirms, userID)
	return p.action, ok && time.Since(p.at) <= confirmTTL
}

// confirmByText: ответ на вопрос «выйти?» / «удалить?» словами. handled=false - это не ответ.
func (h *Handler) confirmByText(ctx context.Context, chatID int64, u domain.User, text string) (bool, error) {
	action, ok := h.takeConfirm(u.TelegramID)
	if !ok {
		return false, nil
	}
	switch strings.Trim(pipeline.Normalize(text), ".!") {
	case "да", "ага", "конечно", "подтверждаю", "да, выйти", "да, удалить", "выйти", "удалить":
		if action == cbLeave {
			return true, h.doLeave(ctx, chatID, u)
		}
		return true, h.doDelete(ctx, chatID, u)
	case "нет", "отмена", "не надо", "стоп", "отменить":
		return true, h.Sender.Send(ctx, chatID, "Хорошо, ничего не меняю.", nil)
	}
	return false, nil
}
