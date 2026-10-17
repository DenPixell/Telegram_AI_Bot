package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", rec.Header().Get("Content-Type"))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestCounterExposition(t *testing.T) {
	c := NewCounterVec("test_events_total", "События.", "kind")
	c.Inc("a")
	c.Add(2, "a")
	c.Inc(`b"x`)
	c.Add(-5, "a") // счётчик не убывает
	out := scrape(t)
	assert.Contains(t, out, "# TYPE test_events_total counter")
	assert.Contains(t, out, `test_events_total{kind="a"} 3`)
	assert.Contains(t, out, `test_events_total{kind="b\"x"} 1`, "кавычки в метках экранируются")
	assert.Equal(t, 3.0, c.Value("a"))
}

func TestHistogramIsCumulative(t *testing.T) {
	h := NewHistogramVec("test_latency_seconds", "Задержка.", []float64{0.1, 1}, "op")
	for _, v := range []float64{0.05, 0.5, 0.7, 5} {
		h.Observe(v, "q")
	}
	out := scrape(t)
	for _, line := range []string{
		`test_latency_seconds_bucket{op="q",le="0.1"} 1`,
		`test_latency_seconds_bucket{op="q",le="1"} 3`,
		`test_latency_seconds_bucket{op="q",le="+Inf"} 4`,
		`test_latency_seconds_sum{op="q"} 6.25`,
		`test_latency_seconds_count{op="q"} 4`,
	} {
		assert.True(t, strings.Contains(out, line), line)
	}
}
