// Package metrics - счётчики и гистограммы в текстовом формате Prometheus без внешних
// зависимостей: метрик немного, а формат экспозиции простой.
//
// Метрики глобальные (как в client_golang), чтобы не протаскивать реестр через все слои.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type metric interface {
	write(w io.Writer)
}

var (
	regMu    sync.Mutex
	registry []metric
)

func register(m metric) {
	regMu.Lock()
	defer regMu.Unlock()
	registry = append(registry, m)
}

// Handler отдаёт все метрики в формате text/plain; version=0.0.4.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		regMu.Lock()
		ms := append([]metric(nil), registry...)
		regMu.Unlock()
		for _, m := range ms {
			m.write(w)
		}
	})
}

// ---------- счётчик с метками ----------

type CounterVec struct {
	name, help string
	labels     []string
	mu         sync.Mutex
	values     map[string]float64 // ключ - значения меток через \x00
}

func NewCounterVec(name, help string, labels ...string) *CounterVec {
	c := &CounterVec{name: name, help: help, labels: labels, values: map[string]float64{}}
	register(c)
	return c
}

// Inc увеличивает счётчик для значений меток (в порядке объявления).
func (c *CounterVec) Inc(values ...string) { c.Add(1, values...) }

func (c *CounterVec) Add(v float64, values ...string) {
	if v < 0 {
		return // счётчик не убывает
	}
	k := key(values)
	c.mu.Lock()
	c.values[k] += v
	c.mu.Unlock()
}

// Value - текущее значение (для тестов).
func (c *CounterVec) Value(values ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.values[key(values)]
}

func (c *CounterVec) write(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	for _, k := range sortedKeys(c.values) {
		_, _ = fmt.Fprintf(w, "%s%s %s\n", c.name, labelString(c.labels, split(k), "", ""), fmtFloat(c.values[k]))
	}
}

// ---------- гистограмма с метками ----------

type HistogramVec struct {
	name, help string
	labels     []string
	buckets    []float64
	mu         sync.Mutex
	series     map[string]*histogram
}

type histogram struct {
	counts []uint64 // по бакетам, не накопительно
	sum    float64
	count  uint64
}

// DurationBuckets - секунды: от 5 мс (БД, HTTP) до 30 с (LLM с ретраями).
var DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

func NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	h := &HistogramVec{name: name, help: help, labels: labels, buckets: buckets, series: map[string]*histogram{}}
	register(h)
	return h
}

func (h *HistogramVec) Observe(v float64, values ...string) {
	k := key(values)
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.series[k]
	if !ok {
		s = &histogram{counts: make([]uint64, len(h.buckets))}
		h.series[k] = s
	}
	for i, b := range h.buckets {
		if v <= b {
			s.counts[i]++
			break
		}
	}
	s.sum += v
	s.count++
}

func (h *HistogramVec) ObserveDuration(d time.Duration, values ...string) {
	h.Observe(d.Seconds(), values...)
}

func (h *HistogramVec) write(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	keys := make([]string, 0, len(h.series))
	for k := range h.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, vals := h.series[k], split(k)
		var cum uint64
		for i, b := range h.buckets {
			cum += s.counts[i]
			_, _ = fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, labelString(h.labels, vals, "le", fmtFloat(b)), cum)
		}
		_, _ = fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, labelString(h.labels, vals, "le", "+Inf"), s.count)
		_, _ = fmt.Fprintf(w, "%s_sum%s %s\n", h.name, labelString(h.labels, vals, "", ""), fmtFloat(s.sum))
		_, _ = fmt.Fprintf(w, "%s_count%s %d\n", h.name, labelString(h.labels, vals, "", ""), s.count)
	}
}

// ---------- формат ----------

func key(values []string) string { return strings.Join(values, "\x00") }

func split(k string) []string {
	if k == "" {
		return nil
	}
	return strings.Split(k, "\x00")
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func labelString(names, values []string, extraName, extraValue string) string {
	var parts []string
	for i, n := range names {
		v := ""
		if i < len(values) {
			v = values[i]
		}
		parts = append(parts, n+`="`+escape(v)+`"`)
	}
	if extraName != "" {
		parts = append(parts, extraName+`="`+extraValue+`"`)
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escape(s string) string { return escaper.Replace(s) }

func fmtFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
