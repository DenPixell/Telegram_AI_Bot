// Package onboarding - конечный автомат регистрации:
//
//	new -> awaiting_choice -> awaiting_family_name -> awaiting_tz -> active
//	new/awaiting_choice -(код или ссылка)-> awaiting_tz -> active
//
// Состояние хранится в users.state и переживает рестарт. Пока пользователь не active,
// его сообщения не уходят в LLM. Кнопки - только для удобства, у каждой есть текстовый аналог.
package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"expense-bot/internal/domain"
	"expense-bot/internal/pipeline"
	"expense-bot/internal/service"
	"expense-bot/internal/storage/postgres"
	"expense-bot/internal/timezone"
)

// Button - inline-кнопка: Data приходит обратно в Callback.
type Button struct {
	Text string
	Data string
}

// Reply - ответ, не зависящий от транспорта.
type Reply struct {
	Text            string
	Buttons         [][]Button
	RequestLocation bool // клавиатура «📍 Отправить геолокацию» + «Пропустить»
	// Pending - трата, которую человек прислал до регистрации; после её завершения её нужно
	// записать (адаптер передаёт текст агенту).
	Pending        string
	RemoveKeyboard bool
}

// Данные кнопок.
const (
	CbCreate  = "onb:create"
	CbHasCode = "onb:code"
	CbSkip    = "onb:skip"
)

type FSM struct {
	store       *postgres.Store
	families    *service.Families
	appTZ       string
	botUsername func() string
	inviteTTL   time.Duration
	now         func() time.Time
}

func New(store *postgres.Store, families *service.Families, appTZ string, inviteTTL time.Duration, botUsername func() string) *FSM {
	return &FSM{store: store, families: families, appTZ: appTZ, inviteTTL: inviteTTL, botUsername: botUsername, now: time.Now}
}

type stateData struct {
	TZAttempts int    `json:"tz_attempts,omitempty"`
	Pending    string `json:"pending,omitempty"` // трата из первого сообщения, переживает рестарт
}

// Start - команда /start с необязательным кодом из deep link. Работает в любом состоянии.
func (f *FSM) Start(ctx context.Context, u domain.User, payload string) (Reply, error) {
	payload = strings.TrimSpace(payload)
	if fam, inFamily, err := f.family(ctx, u.TelegramID); err != nil {
		return Reply{}, err
	} else if inFamily {
		if u.State != domain.StateActive && u.State != domain.StateAwaitingTZ {
			// Несогласованность (например, рестарт посреди шага): членство главнее.
			if err := f.setState(ctx, u.TelegramID, domain.StateAwaitingTZ, stateData{}); err != nil {
				return Reply{}, err
			}
			return f.askTZ(ctx, u.TelegramID, "")
		}
		if payload != "" {
			return Reply{Text: fmt.Sprintf("Вы уже состоите в семье «%s». Чтобы перейти в другую, сначала напишите «выйти из семьи».", familyName(fam))}, nil
		}
		if u.State == domain.StateAwaitingTZ {
			return f.askTZ(ctx, u.TelegramID, "")
		}
		return Reply{Text: "С возвращением! " + usageHint}, nil
	}
	if payload != "" {
		return f.join(ctx, u.TelegramID, payload)
	}
	if err := f.setState(ctx, u.TelegramID, domain.StateAwaitingChoice, stateData{}); err != nil {
		return Reply{}, err
	}
	return welcome(), nil
}

// HandleText - текст пользователя, который ещё не завершил регистрацию.
func (f *FSM) HandleText(ctx context.Context, u domain.User, text string) (Reply, error) {
	norm := strings.TrimRight(pipeline.Normalize(text), ".!")
	switch u.State {
	case domain.StateAwaitingFamilyName:
		if isSkip(norm) {
			return f.create(ctx, u.TelegramID, "")
		}
		if norm == "" {
			return askName(), nil
		}
		return f.create(ctx, u.TelegramID, text)
	case domain.StateAwaitingTZ:
		return f.handleTZ(ctx, u, text, norm)
	default: // new, awaiting_choice
		switch {
		case isCreate(norm):
			return f.toName(ctx, u.TelegramID)
		case isHasCode(norm):
			return Reply{Text: msgAskCode}, nil
		}
		if _, ok := service.NormalizeInviteCode(text); ok {
			return f.join(ctx, u.TelegramID, text)
		}
		if u.State == domain.StateNew {
			if err := f.setState(ctx, u.TelegramID, domain.StateAwaitingChoice, stateData{}); err != nil {
				return Reply{}, err
			}
		}
		return welcome(), nil
	}
}

// HandleLocation - геолокация на шаге часового пояса. Координаты не сохраняются.
func (f *FSM) HandleLocation(ctx context.Context, u domain.User, lat, lon float64) (Reply, error) {
	if u.State != domain.StateAwaitingTZ {
		return Reply{Text: "Геолокация нужна только для часового пояса. " + usageHint}, nil
	}
	z, err := timezone.FromLocation(lat, lon)
	if err != nil {
		return f.tzFailed(ctx, u)
	}
	return f.finish(ctx, u.TelegramID, z)
}

// Callback - нажатие inline-кнопки онбординга.
func (f *FSM) Callback(ctx context.Context, u domain.User, data string) (Reply, error) {
	switch data {
	case CbCreate:
		if _, inFamily, err := f.family(ctx, u.TelegramID); err != nil || inFamily {
			return Reply{Text: "Вы уже в семье."}, err
		}
		return f.toName(ctx, u.TelegramID)
	case CbHasCode:
		return Reply{Text: msgAskCode}, nil
	case CbSkip:
		return f.HandleText(ctx, u, "пропустить")
	}
	return Reply{}, nil
}

func (f *FSM) toName(ctx context.Context, userID int64) (Reply, error) {
	if err := f.setState(ctx, userID, domain.StateAwaitingFamilyName, stateData{}); err != nil {
		return Reply{}, err
	}
	return askName(), nil
}

func (f *FSM) create(ctx context.Context, userID int64, name string) (Reply, error) {
	fam, inv, err := f.families.CreateFamily(ctx, userID, name)
	if errors.Is(err, domain.ErrValidation) {
		return Reply{Text: fmt.Sprintf("Название слишком длинное, до %d символов. Как назовём семью?", service.MaxFamilyNameLen),
			Buttons: [][]Button{{{Text: "Пропустить", Data: CbSkip}}}}, nil
	}
	if errors.Is(err, domain.ErrInFamily) {
		return f.askTZ(ctx, userID, "Вы уже в семье.")
	}
	if err != nil {
		return Reply{}, err
	}
	if err := f.setState(ctx, userID, domain.StateAwaitingTZ, stateData{}); err != nil {
		return Reply{}, err
	}
	head := fmt.Sprintf("Семья «%s» создана, вы — глава семьи 👑\n\n%s", familyName(fam), InviteText(f.botUsername(), inv, f.inviteTTL))
	return f.askTZ(ctx, userID, head)
}

func (f *FSM) join(ctx context.Context, userID int64, code string) (Reply, error) {
	fam, err := f.families.JoinByCode(ctx, userID, code)
	switch {
	case errors.Is(err, domain.ErrTooManyAttempts):
		return Reply{Text: "Слишком много неудачных попыток ввода кода. Попробуйте через час."}, nil
	case errors.Is(err, domain.ErrInviteInvalid):
		if serr := f.setState(ctx, userID, domain.StateAwaitingChoice, stateData{}); serr != nil {
			return Reply{}, serr
		}
		return Reply{Text: "Код не найден или больше не действует. Проверьте его или попросите новый у главы семьи.",
			Buttons: choiceButtons()}, nil
	case errors.Is(err, domain.ErrFamilyFull):
		return Reply{Text: "В этой семье уже максимум участников. Попросите главу семьи освободить место.", Buttons: choiceButtons()}, nil
	case errors.Is(err, domain.ErrInFamily):
		return Reply{Text: "Вы уже состоите в семье. Чтобы перейти в другую, сначала напишите «выйти из семьи»."}, nil
	case err != nil:
		return Reply{}, err
	}
	if err := f.setState(ctx, userID, domain.StateAwaitingTZ, stateData{}); err != nil {
		return Reply{}, err
	}
	return f.askTZ(ctx, userID, fmt.Sprintf("Вы присоединились к семье «%s» 🎉", familyName(fam)))
}

func (f *FSM) handleTZ(ctx context.Context, u domain.User, text, norm string) (Reply, error) {
	if isSkip(norm) {
		return f.finish(ctx, u.TelegramID, timezone.Zone{})
	}
	z, err := timezone.Parse(text)
	if err != nil {
		return f.tzFailed(ctx, u)
	}
	return f.finish(ctx, u.TelegramID, z)
}

// tzFailed: переспрашиваем один раз, затем берём пояс семьи.
func (f *FSM) tzFailed(ctx context.Context, u domain.User) (Reply, error) {
	var sd stateData
	_ = json.Unmarshal(u.StateData, &sd)
	if sd.TZAttempts >= 1 {
		r, err := f.finish(ctx, u.TelegramID, timezone.Zone{})
		r.Text = "Не получилось распознать, поставил часовой пояс семьи. Поменять можно в любой момент: «мой часовой пояс Новосибирск».\n\n" + r.Text
		return r, err
	}
	sd.TZAttempts++
	if err := f.setState(ctx, u.TelegramID, domain.StateAwaitingTZ, sd); err != nil {
		return Reply{}, err
	}
	return Reply{Text: "Не узнал такой часовой пояс. Напишите город или смещение, например «Екатеринбург» или «UTC+5», или нажмите «Пропустить».",
		RequestLocation: true}, nil
}

// finish завершает регистрацию. Пустая зона - пояс семьи. Если пользователь - единственный
// участник (только что создал семью), его пояс становится и поясом семьи.
func (f *FSM) finish(ctx context.Context, userID int64, z timezone.Zone) (Reply, error) {
	a, err := f.families.Actor(ctx, userID)
	if err != nil {
		return Reply{}, err
	}
	fam, err := f.families.GetFamily(ctx, a)
	if err != nil {
		return Reply{}, err
	}
	if z.Name != "" {
		if err := f.store.SetUserTimezone(ctx, userID, z.Name); err != nil {
			return Reply{}, err
		}
		if n, err := f.store.CountMembers(ctx, a.FamilyID); err == nil && n == 1 && a.IsOwner() && fam.DefaultTZ != z.Name {
			if _, err := f.families.UpdateFamily(ctx, a, fam.Name, z.Name); err != nil {
				return Reply{}, err
			}
		}
	} else {
		z = zoneOrUTC(fam.DefaultTZ, f.appTZ)
	}
	pending := f.pending(ctx, userID)
	// Активное состояние - с чистыми данными: отложенная трата уходит в ответ, а не остаётся.
	if err := f.store.SetUserState(ctx, userID, domain.StateActive, nil); err != nil {
		return Reply{}, err
	}
	text := "Готово! Часовой пояс: " + z.Describe(f.now()) + ".\n\n" + Help(a.IsOwner())
	return Reply{Text: text, RemoveKeyboard: true, Pending: pending}, nil
}

func (f *FSM) askTZ(ctx context.Context, userID int64, head string) (Reply, error) {
	def := ""
	if a, err := f.families.Actor(ctx, userID); err == nil {
		if fam, err := f.families.GetFamily(ctx, a); err == nil {
			def = zoneOrUTC(fam.DefaultTZ, f.appTZ).Describe(f.now())
		}
	}
	text := "В каком вы часовом поясе? Напишите город («Новосибирск»), смещение («МСК+4», «UTC+7») или отправьте геолокацию кнопкой ниже."
	if def != "" {
		text += " Можно пропустить — тогда будет " + def + "."
	}
	if head != "" {
		text = head + "\n\n" + text
	}
	return Reply{Text: text, RequestLocation: true}, nil
}

func (f *FSM) family(ctx context.Context, userID int64) (domain.Family, bool, error) {
	a, err := f.families.Actor(ctx, userID)
	if errors.Is(err, domain.ErrNotInFamily) {
		return domain.Family{}, false, nil
	}
	if err != nil {
		return domain.Family{}, false, err
	}
	fam, err := f.families.GetFamily(ctx, a)
	return fam, err == nil, err
}

// Remember запоминает трату, присланную до регистрации («кофе 350» первым сообщением),
// и показывает выбор семьи. Трата будет записана сразу после регистрации.
func (f *FSM) Remember(ctx context.Context, u domain.User, text string) (Reply, error) {
	state := u.State
	if state == domain.StateNew {
		state = domain.StateAwaitingChoice
	}
	if err := f.setState(ctx, u.TelegramID, state, stateData{Pending: text}); err != nil {
		return Reply{}, err
	}
	r := welcome()
	r.Text = fmt.Sprintf("Запишу «%s», как только вы создадите семью или вступите в неё — это займёт минуту.\n\n", text) + r.Text
	return r, nil
}

// pending - отложенная трата пользователя, если есть.
func (f *FSM) pending(ctx context.Context, userID int64) string {
	u, err := f.store.GetUser(ctx, userID)
	if err != nil {
		return ""
	}
	var sd stateData
	_ = json.Unmarshal(u.StateData, &sd)
	return sd.Pending
}

// setState сохраняет шаг онбординга. Отложенная трата переносится между шагами,
// пока её явно не заменят.
func (f *FSM) setState(ctx context.Context, userID int64, s domain.UserState, sd stateData) error {
	if sd.Pending == "" {
		sd.Pending = f.pending(ctx, userID)
	}
	b, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	return f.store.SetUserState(ctx, userID, s, b)
}

func zoneOrUTC(names ...string) timezone.Zone {
	for _, n := range names {
		if z, err := timezone.Parse(n); err == nil {
			return z
		}
	}
	loc := timezone.Resolve(names...)
	return timezone.Zone{Name: loc.String(), Label: loc.String(), Location: loc}
}

func familyName(f domain.Family) string {
	if f.Name == "" {
		return "без названия"
	}
	return f.Name
}

// ---------- тексты ----------

const (
	msgAskCode = "Пришлите код приглашения, например K7M2Q-9XH4T. Его можно получить у главы семьи: он пишет боту «ссылка»."
	usageHint  = "Пишите траты, например «кофе 350». Все команды — «помощь»."
)

// Welcome - первый экран: создать семью или ввести код.
func Welcome() Reply { return welcome() }

func welcome() Reply {
	return Reply{
		Text: "Привет! Я помогу вести учёт семейных расходов: просто пишите траты обычными словами, например «кофе 350» или «такси 1200 вчера».\n\n" +
			"Для начала создайте семью или присоединитесь к существующей по коду приглашения. Можно ответить словами: «создать» или прислать код.",
		Buttons: choiceButtons(),
	}
}

func choiceButtons() [][]Button {
	return [][]Button{{{Text: "Создать семью", Data: CbCreate}, {Text: "У меня есть код", Data: CbHasCode}}}
}

func askName() Reply {
	return Reply{Text: "Как назовём семью? Например: «Ивановы». Или нажмите «Пропустить».",
		Buttons: [][]Button{{{Text: "Пропустить", Data: CbSkip}}}}
}

// Help - список возможностей. Владельцу - и команды управления семьёй.
func Help(owner bool) string {
	s := "Как пользоваться:\n" +
		"• «кофе 350» — записать трату\n" +
		"• «такси 1200 вчера», «продукты 2400 в пятницу» — с датой\n" +
		"• «кофе 350 и обед 600» — несколько трат сразу\n" +
		"• «сколько потратил на еду в этом месяце», «покажи траты за неделю»\n" +
		"• «удали последнюю», «последнее было не 350, а 450»\n" +
		"• «мой часовой пояс Новосибирск» — сменить часовой пояс\n" +
		"• «выйти из семьи», «удалить мои данные»\n"
	if owner {
		s += "\nДля главы семьи:\n" +
			"• «ссылка» — пригласить близких\n" +
			"• «панель» — веб-панель со всеми тратами семьи\n"
	}
	s += "\nВы видите только свои траты, глава семьи — траты всей семьи. В ИИ уходит только текст сообщения о трате, без имён."
	return s
}

// InviteText - приглашение: ссылка (если известен username бота) и код для ручного ввода.
func InviteText(botUsername string, inv domain.Invite, ttl time.Duration) string {
	days := int(ttl.Hours() / 24)
	var b strings.Builder
	b.WriteString("Пригласить близких:\n")
	if botUsername != "" {
		b.WriteString(service.InviteLink(botUsername, inv.Code) + "\n")
	}
	fmt.Fprintf(&b, "Или код: %s\nДействует %d дн., до %d человек.", service.FormatInviteCode(inv.Code), max(days, 1), inv.MaxUses)
	return b.String()
}

func isSkip(s string) bool {
	switch s {
	case "пропустить", "пропуск", "skip", "дальше", "не знаю", "-":
		return true
	}
	return false
}

func isCreate(s string) bool {
	switch s {
	case "создать", "создать семью", "новая семья", "создай", "create":
		return true
	}
	return false
}

func isHasCode(s string) bool {
	switch s {
	case "код", "у меня есть код", "есть код", "ввести код", "вступить", "присоединиться":
		return true
	}
	return false
}
