package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"expense-bot/internal/domain"
	"expense-bot/internal/service"
)

func (s *Server) categories(w http.ResponseWriter, _ *http.Request, _ domain.Actor) {
	writeJSON(w, http.StatusOK, map[string]any{"items": domain.Categories})
}

// listExpenses: GET /api/expenses?member_id=&from=&to=&category=&q=&limit=&offset=
// category можно передать несколько раз или через запятую.
func (s *Server) listExpenses(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	scope, err := service.PanelScope(a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	f := domain.ExpenseFilter{Scope: scope}

	if v := q.Get("member_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			badRequest(w, "invalid member_id")
			return
		}
		f.MemberID = n
	}
	for name, dst := range map[string]*domain.Date{"from": &f.From, "to": &f.To} {
		if v := q.Get(name); v != "" {
			d, err := domain.ParseDate(v)
			if err != nil {
				badRequest(w, "invalid %s: expected YYYY-MM-DD", name)
				return
			}
			*dst = d
		}
	}
	for _, raw := range q["category"] {
		for _, c := range strings.Split(raw, ",") {
			cat := domain.Category(strings.TrimSpace(c))
			if !cat.Valid() {
				badRequest(w, "unknown category %q", c)
				return
			}
			f.Categories = append(f.Categories, cat)
		}
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		f.Terms = s.ExpandTerms(v) // «рестик» найдёт и «ресторан»
	}
	for name, dst := range map[string]*int{"limit": &f.Limit, "offset": &f.Offset} {
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				badRequest(w, "invalid %s", name)
				return
			}
			*dst = n
		}
	}
	if f.Limit == 0 {
		f.Limit = domain.DefaultLimit
	}
	f.Limit = min(f.Limit, domain.MaxLimit)

	items, total, count, err := s.Expenses.List(r.Context(), f)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, expenseListDTO{
		Items: mapSlice(items, toExpenseDTO), Total: total, Count: count, Limit: f.Limit, Offset: f.Offset,
	})
}

func (s *Server) getExpense(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	scope, err := service.PanelScope(a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	e, err := s.Expenses.Get(r.Context(), scope, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toExpenseDTO(e))
}

type createExpenseRequest struct {
	UserID   int64        `json:"user_id"` // член семьи, чья трата; 0 = сам владелец
	Amount   domain.Money `json:"amount"`
	Category string       `json:"category"`
	Date     domain.Date  `json:"date"` // необязательно, по умолчанию сегодня
	Title    string       `json:"title"`
	Comment  string       `json:"comment"`
}

func (s *Server) createExpense(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	if _, err := service.PanelScope(a); err != nil {
		writeError(w, r, err)
		return
	}
	var req createExpenseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	e, err := s.Expenses.Create(r.Context(), a, domain.Expense{
		UserID: req.UserID, Amount: req.Amount, Category: domain.Category(req.Category),
		SpentAt: req.Date, Title: req.Title, Comment: req.Comment,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toExpenseDTO(e))
}

type updateExpenseRequest struct {
	Version  int          `json:"version"` // из последнего GET; при несовпадении 409
	Amount   domain.Money `json:"amount"`
	Category string       `json:"category"`
	Date     domain.Date  `json:"date"`
	Title    string       `json:"title"`
	Comment  string       `json:"comment"`
}

func (s *Server) updateExpense(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	scope, err := service.PanelScope(a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req updateExpenseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Version <= 0 {
		badRequest(w, "version is required")
		return
	}
	e, err := s.Expenses.Update(r.Context(), scope, domain.Expense{
		ID: id, Version: req.Version, Amount: req.Amount, Category: domain.Category(req.Category),
		SpentAt: req.Date, Title: req.Title, Comment: req.Comment,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toExpenseDTO(e))
}

func (s *Server) deleteExpense(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	scope, err := service.PanelScope(a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.Expenses.Delete(r.Context(), scope, id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
