package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"expense-bot/internal/domain"
)

// Broadcaster - рассылка всем пользователям (telegram.Broadcaster).
type Broadcaster interface {
	Send(ctx context.Context, text string) (domain.Broadcast, error)
	Status(ctx context.Context, id int64) (domain.Broadcast, error)
}

type broadcastDTO struct {
	ID         int64      `json:"id"`
	Status     string     `json:"status"`
	Text       string     `json:"text"`
	Total      int        `json:"total"`
	Sent       int        `json:"sent"`
	Failed     int        `json:"failed"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func toBroadcastDTO(b domain.Broadcast) broadcastDTO {
	return broadcastDTO{ID: b.ID, Status: string(b.Status), Text: b.Text, Total: b.Total, Sent: b.Sent, Failed: b.Failed,
		CreatedAt: b.CreatedAt.UTC(), StartedAt: b.StartedAt, FinishedAt: b.FinishedAt}
}

// adminBroadcast: POST /admin/broadcast {"text": "..."} - отправить всем активным пользователям.
// Отвечает 202 сразу; ход рассылки - GET /admin/broadcast/{id}.
func (s *Server) adminBroadcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	b, err := s.Broadcast.Send(r.Context(), req.Text)
	switch {
	case errors.Is(err, domain.ErrConflict):
		writeAPIError(w, http.StatusConflict, "conflict", "Уже идёт другая рассылка")
	case err != nil:
		writeError(w, r, err)
	default:
		writeJSON(w, http.StatusAccepted, toBroadcastDTO(b))
	}
}

func (s *Server) adminBroadcastStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	b, err := s.Broadcast.Status(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBroadcastDTO(b))
}
