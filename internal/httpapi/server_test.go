package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestHealthAndReady(t *testing.T) {
	cases := []struct {
		path string
		db   error
		want int
	}{
		{"/healthz", errors.New("down"), http.StatusOK},
		{"/readyz", nil, http.StatusOK},
		{"/readyz", errors.New("down"), http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		New(Deps{DB: fakePinger{tc.db}, AuthMode: "magic_link"}).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s (db err %v): code %d, want %d", tc.path, tc.db, rec.Code, tc.want)
		}
	}
}
