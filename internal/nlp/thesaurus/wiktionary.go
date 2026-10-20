package thesaurus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"expense-bot/internal/nlp/textnorm"
)

// Related - связанные слова статьи. Гиперонимы надёжнее: синонимы многозначных слов
// уводят в сторону («день» -> «свет» -> электричество).
type Related struct {
	Hypernyms []string
	Synonyms  []string
}

// DefaultWiktionaryURL - API русского Викисловаря (бесплатно, без ключа).
const DefaultWiktionaryURL = "https://ru.wiktionary.org/w/api.php"

// Wiktionary читает разделы «Гиперонимы» и «Синонимы» русских статей Викисловаря.
// Отправляется только одно слово из сообщения, не текст целиком.
type Wiktionary struct {
	endpoint string
	client   *http.Client
}

func NewWiktionary(endpoint string, timeout time.Duration) *Wiktionary {
	if endpoint == "" {
		endpoint = DefaultWiktionaryURL
	}
	return &Wiktionary{endpoint: endpoint, client: &http.Client{Timeout: timeout}}
}

// Правила Wikimedia требуют осмысленный User-Agent с контактом.
const wikiUserAgent = "expense-bot/1.0 (https://github.com/DenPixell/Telegram_AI_Bot)"

const maxWikiResponse = 4 << 20

type wikiResponse struct {
	Query struct {
		Redirects []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"redirects"`
		Normalized []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"normalized"`
		Pages []struct {
			Title     string `json:"title"`
			Missing   bool   `json:"missing"`
			Revisions []struct {
				Slots struct {
					Main struct {
						Content string `json:"content"`
					} `json:"main"`
				} `json:"slots"`
			} `json:"revisions"`
		} `json:"pages"`
	} `json:"query"`
}

// Lookup делает один запрос на все заголовки (до 50 по ограничению API).
func (w *Wiktionary) Lookup(ctx context.Context, titles []string) (map[string]Related, error) {
	if len(titles) == 0 {
		return nil, nil
	}
	q := url.Values{
		"action": {"query"}, "prop": {"revisions"}, "rvprop": {"content"}, "rvslots": {"main"},
		"format": {"json"}, "formatversion": {"2"}, "redirects": {"1"},
		"titles": {strings.Join(titles[:min(len(titles), 50)], "|")},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("wiktionary request: %w", err)
	}
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wiktionary: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wiktionary: status %d", resp.StatusCode)
	}
	var wr wikiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxWikiResponse)).Decode(&wr); err != nil {
		return nil, fmt.Errorf("wiktionary decode: %w", err)
	}

	// Заголовок из ответа -> запрошенный заголовок (через нормализацию и редиректы).
	alias := map[string]string{}
	for _, t := range titles {
		alias[t] = t
	}
	// Точный заголовок важнее редиректа: при запросе «собака» и «собаки» вторая форма
	// перенаправляет на «собака», и без этой проверки статья приписалась бы «собаки».
	requested := map[string]bool{}
	for _, t := range titles {
		requested[t] = true
	}
	for _, n := range wr.Query.Normalized {
		if orig, ok := alias[n.From]; ok && !requested[n.To] {
			alias[n.To] = orig
		}
	}
	for _, r := range wr.Query.Redirects {
		if orig, ok := alias[r.From]; ok && !requested[r.To] {
			alias[r.To] = orig
		}
	}
	res := map[string]Related{}
	for _, p := range wr.Query.Pages {
		if p.Missing || len(p.Revisions) == 0 {
			continue
		}
		orig, ok := alias[p.Title]
		if !ok {
			continue
		}
		res[orig] = relatedFromWikitext(p.Revisions[0].Slots.Main.Content)
	}
	return res, nil
}

var (
	wikiHeader = regexp.MustCompile(`^=+\s*([^=]+?)\s*=+\s*$`)
	wikiLink   = regexp.MustCompile(`\[\[([^\]|#]+)(?:[|#][^\]]*)?\]\]`)
)

// relatedFromWikitext извлекает ссылки из разделов «Гиперонимы» и «Синонимы» русской секции.
func relatedFromWikitext(text string) Related {
	if i := strings.Index(text, "{{-ru-}}"); i >= 0 {
		text = text[i:]
		// Следующая языковая секция: «= {{-en-}} =».
		if j := strings.Index(text[len("{{-ru-}}"):], "\n= {{-"); j >= 0 {
			text = text[:j+len("{{-ru-}}")]
		}
	}
	var hyper, syn []string
	var cur *[]string
	for _, line := range strings.Split(text, "\n") {
		if m := wikiHeader.FindStringSubmatch(line); m != nil {
			switch strings.ToLower(m[1]) {
			case "гиперонимы":
				cur = &hyper
			case "синонимы":
				cur = &syn
			default:
				cur = nil
			}
			continue
		}
		if cur == nil {
			continue
		}
		for _, l := range wikiLink.FindAllStringSubmatch(line, -1) {
			w := textnorm.Normalize(strings.TrimSpace(l[1]))
			if w != "" && textnorm.IsCyrillic(w) && len([]rune(w)) <= 40 {
				*cur = append(*cur, w)
			}
		}
	}
	return Related{Hypernyms: uniq(hyper), Synonyms: uniq(syn)}
}

func uniq(ws []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range ws {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// lemmaCandidates - вероятные начальные формы слова для поиска статьи:
// «шаурмой» -> шаурмой, шаурма, шаурм, ... Порядок - от более вероятных.
func LemmaCandidates(word string) []string {
	stem := textnorm.Stem(word)
	cands := []string{word}
	for _, end := range []string{"а", "", "о", "ь", "я", "е", "й", "ы", "и"} {
		cands = append(cands, stem+end)
	}
	seen := map[string]bool{}
	out := cands[:0]
	for _, c := range cands {
		if len([]rune(c)) >= 3 && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
