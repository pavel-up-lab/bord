package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"bord/internal/domain"
)

func (h *Handler) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(w, r, domain.NewError(domain.CodeUnauthorized, "", errors.New("missing credential")))
			return
		}
		if err := h.config.Admin.Authenticate(r.Context(), parts[1], RequestID(r.Context())); err != nil {
			writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) adminListReports(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := h.pagination(w, r, "admin:reports")
	if !ok {
		return
	}
	page, err := h.config.Admin.ListReports(r.Context(), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if page.Items == nil {
		page.Items = []AdminReport{}
	}
	h.writePage(w, r, "admin:reports", "reports", page.Items, page.NextCursor)
}

func (h *Handler) adminSetVisibility(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("post_id")
	if !validResourceID(id) {
		writeError(w, r, validation("post_id", errors.New("invalid ID")))
		return
	}
	var body struct {
		Visible bool   `json:"visible"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if !validReason(body.Reason) {
		writeError(w, r, validation("reason", errors.New("invalid reason")))
		return
	}
	if err := h.config.Admin.SetPostVisibility(r.Context(), id, body.Visible, body.Reason, RequestID(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) adminCloseThread(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("thread_id")
	if !validResourceID(id) {
		writeError(w, r, validation("thread_id", errors.New("invalid ID")))
		return
	}
	reason, ok := h.adminReason(w, r)
	if !ok {
		return
	}
	if err := h.config.Admin.CloseThread(r.Context(), id, reason, RequestID(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) adminCreateBan(w http.ResponseWriter, r *http.Request) {
	var body BanRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if !validReason(body.Reason) || body.Subject == "" || len(body.Subject) > 256 || len(body.Operations) < 1 || len(body.Operations) > 10 || !body.ExpiresAt.After(timeNow()) || body.SubjectType != "ip" && body.SubjectType != "prefix" && body.SubjectType != "lookup" {
		writeError(w, r, validation("body", errors.New("invalid ban")))
		return
	}
	ban, err := h.config.Admin.CreateBan(r.Context(), body, RequestID(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ban)
}

func (h *Handler) adminRevokeBan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("ban_id")
	if !validResourceID(id) {
		writeError(w, r, validation("ban_id", errors.New("invalid ID")))
		return
	}
	reason, ok := h.adminReason(w, r)
	if !ok {
		return
	}
	if err := h.config.Admin.RevokeBan(r.Context(), id, reason, RequestID(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) adminRevealIP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("post_id")
	if !validResourceID(id) {
		writeError(w, r, validation("post_id", errors.New("invalid ID")))
		return
	}
	reason, ok := h.adminReason(w, r)
	if !ok {
		return
	}
	ip, err := h.config.Admin.RevealPostIP(r.Context(), id, reason, RequestID(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ip": ip})
}

func (h *Handler) adminReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return "", false
	}
	if !validReason(body.Reason) {
		writeError(w, r, validation("reason", errors.New("invalid reason")))
		return "", false
	}
	return body.Reason, true
}

func validReason(reason string) bool { return len(reason) >= 3 && len(reason) <= 500 }

var timeNow = func() time.Time { return time.Now() }
