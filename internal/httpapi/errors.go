package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"expense-bot/internal/domain"
	"expense-bot/internal/service"
)

const maxBodyBytes = 64 << 10

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	var e apiError
	e.Error.Code, e.Error.Message = code, message
	writeJSON(w, status, e)
}

// writeError переводит доменную ошибку в HTTP-ответ. Внутренние подробности клиенту не уходят,
// кроме текста ошибок валидации.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrUnauthenticated):
		writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "Нужно войти: напишите боту «панель»")
	case errors.Is(err, domain.ErrValidation):
		writeAPIError(w, http.StatusBadRequest, "validation", validationMessage(err))
	case errors.Is(err, domain.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "Запись не найдена")
	case errors.Is(err, domain.ErrForbidden):
		writeAPIError(w, http.StatusForbidden, "forbidden", "Недостаточно прав")
	case errors.Is(err, domain.ErrConflict):
		writeAPIError(w, http.StatusConflict, "conflict", "Запись изменилась, обновите страницу")
	case errors.Is(err, domain.ErrOwnerMustStay):
		writeAPIError(w, http.StatusConflict, "owner_must_stay", "Сначала передайте владение другому участнику")
	case errors.Is(err, domain.ErrFamilyFull):
		writeAPIError(w, http.StatusConflict, "family_full", "В семье уже максимум участников")
	default:
		slog.ErrorContext(r.Context(), "request failed", "err", err, "request_id", requestID(r.Context()), "path", r.URL.Path)
		writeAPIError(w, http.StatusInternalServerError, "internal", "Внутренняя ошибка, попробуйте позже")
	}
}

// validationMessage оставляет от цепочки ошибок только пояснение после «validation failed: ».
func validationMessage(err error) string {
	msg := err.Error()
	if _, after, ok := strings.Cut(msg, domain.ErrValidation.Error()+": "); ok {
		return after
	}
	return "Некорректные данные"
}

func badRequest(w http.ResponseWriter, format string, args ...any) {
	writeAPIError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf(format, args...))
}

// decodeJSON читает тело как один JSON-объект без лишних полей и с ограничением размера.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		badRequest(w, "invalid JSON body: %v", err)
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		badRequest(w, "body must contain a single JSON object")
		return false
	}
	return true
}

// pathID разбирает положительный числовой идентификатор из пути.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		badRequest(w, "invalid %s", name)
		return 0, false
	}
	return id, true
}
