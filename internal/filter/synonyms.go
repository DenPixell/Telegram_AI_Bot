// Нормализация разговорных названий: "рестик" == "ресторан".
package filter

var synonyms = map[string]string{
	"рестик":      "ресторан",
	"ресторанчик": "ресторан",
	"кофеек":      "кофе",
	"шава":        "шаурма",
}

func Normalize(word string) string {
	if v, ok := synonyms[word]; ok {
		return v
	}
	return word
}
