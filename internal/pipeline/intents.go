package pipeline

import "strings"

// IntentKind - служебное намерение, которое обрабатывается без LLM (ступень 3).
type IntentKind string

const (
	IntentHelp       IntentKind = "help"
	IntentInvite     IntentKind = "invite"      // владельцу: ссылка-приглашение
	IntentPanel      IntentKind = "panel"       // владельцу: ссылка входа в панель
	IntentTimezone   IntentKind = "timezone"    // Arg - город, смещение или IANA
	IntentLeave      IntentKind = "leave"       // выйти из семьи
	IntentDeleteData IntentKind = "delete_data" // удалить мои данные
)

type Intent struct {
	Kind IntentKind
	Arg  string
}

var exactIntents = map[string]IntentKind{
	"помощь": IntentHelp, "help": IntentHelp, "справка": IntentHelp, "что ты умеешь": IntentHelp,
	"как пользоваться": IntentHelp, "команды": IntentHelp,
	"ссылка": IntentInvite, "пригласить": IntentInvite, "приглашение": IntentInvite, "invite": IntentInvite,
	"код приглашения": IntentInvite, "пригласить в семью": IntentInvite, "ссылка-приглашение": IntentInvite,
	"панель": IntentPanel, "panel": IntentPanel, "веб-панель": IntentPanel, "админка": IntentPanel,
	"войти в панель": IntentPanel, "открыть панель": IntentPanel,
	"выйти из семьи": IntentLeave, "покинуть семью": IntentLeave, "leave": IntentLeave,
	"удалить мои данные": IntentDeleteData, "удали мои данные": IntentDeleteData, "deletedata": IntentDeleteData,
	"часовой пояс": IntentTimezone, "мой часовой пояс": IntentTimezone, "tz": IntentTimezone,
}

// Префиксы с аргументом: «мой часовой пояс Новосибирск», «/tz UTC+5».
var prefixIntents = []struct {
	prefix string
	kind   IntentKind
}{
	{"мой часовой пояс ", IntentTimezone},
	{"часовой пояс ", IntentTimezone},
	{"сменить часовой пояс ", IntentTimezone},
	{"tz ", IntentTimezone},
	// «пояс 500» - это покупка пояса, поэтому одиночное «пояс» намерением не считается.
}

// MatchIntent распознаёт служебное намерение по точной фразе или префиксу.
// Команды Telegram («/help», «/tz@my_bot UTC+5») обрабатываются так же.
func MatchIntent(text string) (Intent, bool) {
	s := Normalize(text)
	if cmd, ok := strings.CutPrefix(s, "/"); ok {
		name, arg, _ := strings.Cut(cmd, " ")
		name, _, _ = strings.Cut(name, "@") // /tz@my_bot
		s = strings.TrimSpace(name + " " + arg)
	}
	s = strings.TrimRight(s, ".!?")
	if k, ok := exactIntents[s]; ok {
		return Intent{Kind: k}, true
	}
	for _, p := range prefixIntents {
		if arg, ok := strings.CutPrefix(s, p.prefix); ok && strings.TrimSpace(arg) != "" {
			// Аргумент из исходного текста: регистр важен для IANA-имён.
			orig := strings.TrimSpace(text)
			return Intent{Kind: p.kind, Arg: strings.TrimSpace(orig[len(orig)-len(arg):])}, true
		}
	}
	return Intent{}, false
}
