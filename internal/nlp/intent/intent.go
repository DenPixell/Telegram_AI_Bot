package intent

import (
	"strings"

	"expense-bot/internal/nlp/textnorm"
)

// Kind - служебное намерение, которое обрабатывается без LLM (ступень 3).
type Kind string

const (
	Help       Kind = "help"
	Invite     Kind = "invite"      // владельцу: ссылка-приглашение
	Panel      Kind = "panel"       // владельцу: ссылка входа в панель
	Timezone   Kind = "timezone"    // Arg - город, смещение или IANA
	Leave      Kind = "leave"       // выйти из семьи
	DeleteData Kind = "delete_data" // удалить мои данные
)

type Intent struct {
	Kind Kind
	Arg  string
}

var exactIntents = map[string]Kind{
	"помощь": Help, "help": Help, "справка": Help, "что ты умеешь": Help,
	"как пользоваться": Help, "команды": Help,
	"ссылка": Invite, "пригласить": Invite, "приглашение": Invite, "invite": Invite,
	"код приглашения": Invite, "пригласить в семью": Invite, "ссылка-приглашение": Invite,
	"панель": Panel, "panel": Panel, "веб-панель": Panel, "админка": Panel,
	"войти в панель": Panel, "открыть панель": Panel,
	"выйти из семьи": Leave, "покинуть семью": Leave, "leave": Leave,
	"удалить мои данные": DeleteData, "удали мои данные": DeleteData, "deletedata": DeleteData,
	"часовой пояс": Timezone, "мой часовой пояс": Timezone, "tz": Timezone,
}

// Префиксы с аргументом: «мой часовой пояс Новосибирск», «/tz UTC+5».
var prefixIntents = []struct {
	prefix string
	kind   Kind
}{
	{"мой часовой пояс ", Timezone},
	{"часовой пояс ", Timezone},
	{"сменить часовой пояс ", Timezone},
	{"tz ", Timezone},
	// «пояс 500» - это покупка пояса, поэтому одиночное «пояс» намерением не считается.
}

// MatchIntent распознаёт служебное намерение по точной фразе или префиксу.
// Команды Telegram («/help», «/tz@my_bot UTC+5») обрабатываются так же.
func Match(text string) (Intent, bool) {
	s := textnorm.Normalize(text)
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
