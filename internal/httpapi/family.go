package httpapi

import (
	"net/http"

	"expense-bot/internal/domain"
)

func (s *Server) getFamily(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	f, err := s.Families.GetFamily(r.Context(), a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toFamilyDTO(f))
}

type updateFamilyRequest struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"` // IANA
}

func (s *Server) updateFamily(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	var req updateFamilyRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	f, err := s.Families.UpdateFamily(r.Context(), a, req.Name, req.Timezone)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toFamilyDTO(f))
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	ms, err := s.Families.ListMembers(r.Context(), a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": mapSlice(ms, toMemberDTO)})
}

type updateMemberRequest struct {
	Relation string `json:"relation"`
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var req updateMemberRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Families.SetRelation(r.Context(), a, userID, req.Relation); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	if err := s.Families.RemoveMember(r.Context(), a, userID); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// transferOwnership: после передачи прежний владелец теряет доступ к панели, его сессии удалены.
func (s *Server) transferOwnership(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	if err := s.Families.TransferOwnership(r.Context(), a, userID); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	invs, err := s.Families.ListInvites(r.Context(), a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	bot := s.BotUsername()
	writeJSON(w, http.StatusOK, map[string]any{
		"items": mapSlice(invs, func(i domain.Invite) inviteDTO { return toInviteDTO(i, bot) }),
	})
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	inv, err := s.Families.CreateInvite(r.Context(), a)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toInviteDTO(inv, s.BotUsername()))
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, a domain.Actor) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.Families.RevokeInvite(r.Context(), a, id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
