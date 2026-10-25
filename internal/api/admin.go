package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bord/internal/domain"
	"bord/internal/security"
)

type adminPrincipalContextKey struct{}

func WithAdminPrincipal(ctx context.Context, principal AdminPrincipal) context.Context {
	return context.WithValue(ctx, adminPrincipalContextKey{}, principal)
}

func AdminPrincipalFromContext(ctx context.Context) (AdminPrincipal, bool) {
	principal, ok := ctx.Value(adminPrincipalContextKey{}).(AdminPrincipal)
	return principal, ok
}

func (h *Handler) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			if _, err := h.config.Admin.Authenticate(r.Context(), "", RequestID(r.Context())); err != nil {
				writeError(w, r, err)
				return
			}
			writeError(w, r, domain.NewError(domain.CodeUnauthorized, "", errors.New("missing credential")))
			return
		}
		principal, err := h.config.Admin.Authenticate(r.Context(), parts[1], RequestID(r.Context()))
		if err != nil {
			writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithAdminPrincipal(r.Context(), principal)))
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

func (h *Handler) adminGetSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, domain.NewError(domain.CodeUnauthorized, "", nil))
		return
	}
	role := "reports"
	if principal.Role == security.RoleModerator {
		role = "moderator"
	} else if principal.Role == security.RoleAdministrator {
		role = "administrator"
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": principal.ID, "role": role, "expires_at": principal.ExpiresAt})
}

func (h *Handler) adminGetSystemStorage(w http.ResponseWriter, r *http.Request) {
	principal, ok := AdminPrincipalFromContext(r.Context())
	if !ok {
		writeError(w, r, domain.NewError(domain.CodeUnauthorized, "", nil))
		return
	}
	if !principal.Role.Allows(security.RoleAdministrator) {
		writeError(w, r, domain.NewError(domain.CodeForbidden, "", nil))
		return
	}
	if h.config.DiskUsage == nil || h.config.DiskPath == "" {
		writeError(w, r, unavailable())
		return
	}
	space, err := h.config.DiskUsage(h.config.DiskPath)
	if err != nil || space.TotalBytes < 0 || space.AvailableBytes < 0 || space.AvailableBytes > space.TotalBytes {
		writeError(w, r, unavailable())
		return
	}
	writeJSON(w, http.StatusOK, space)
}

func (h *Handler) adminListBoards(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "all"
	}
	if state != "active" && state != "archived" && state != "all" {
		writeError(w, r, validation("state", errors.New("invalid board state")))
		return
	}
	collection := "admin:boards:" + state
	limit, cursor, ok := h.pagination(w, r, collection)
	if !ok {
		return
	}
	page, err := h.config.Admin.ListBoards(r.Context(), state, cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if page.Items == nil {
		page.Items = []AdminBoard{}
	}
	h.writePage(w, r, collection, "boards", page.Items, page.NextCursor)
}

func (h *Handler) adminCreateBoard(w http.ResponseWriter, r *http.Request) {
	var body AdminCreateBoardRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if !validBoardSlug(body.Slug) || len(strings.TrimSpace(body.Title)) < 1 || len(body.Title) > 100 || strings.TrimSpace(body.Title) != body.Title || !validReason(body.Reason) {
		writeError(w, r, validation("body", errors.New("invalid board")))
		return
	}
	board, err := h.config.Admin.CreateBoard(r.Context(), body, RequestID(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, board)
}

func (h *Handler) adminArchiveBoard(w http.ResponseWriter, r *http.Request) {
	h.adminSetBoardState(w, r, false)
}

func (h *Handler) adminRestoreBoard(w http.ResponseWriter, r *http.Request) {
	h.adminSetBoardState(w, r, true)
}

func (h *Handler) adminSetBoardState(w http.ResponseWriter, r *http.Request, enabled bool) {
	id := r.PathValue("board_id")
	if !validResourceID(id) {
		writeError(w, r, validation("board_id", errors.New("invalid ID")))
		return
	}
	reason, ok := h.adminReason(w, r)
	if !ok {
		return
	}
	var board AdminBoard
	var err error
	if enabled {
		board, err = h.config.Admin.RestoreBoard(r.Context(), id, reason, RequestID(r.Context()))
	} else {
		board, err = h.config.Admin.ArchiveBoard(r.Context(), id, reason, RequestID(r.Context()))
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, board)
}

func (h *Handler) adminGetPostContext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("post_id")
	if !validResourceID(id) {
		writeError(w, r, validation("post_id", errors.New("invalid ID")))
		return
	}
	collection := "admin:post-context:" + id
	limit, cursor, ok := h.pagination(w, r, collection)
	if !ok {
		return
	}
	result, err := h.config.Admin.GetPostContext(r.Context(), id, cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	response := map[string]any{
		"board":          map[string]any{"id": result.Board.ID, "slug": result.Board.Slug, "title": result.Board.Title},
		"thread":         map[string]any{"id": result.Thread.ID, "closed": result.Thread.Closed},
		"target_post_id": result.TargetPost,
		"posts":          result.Posts,
	}
	if result.NextCursor != "" && h.config.CursorCodec != nil {
		next, encodeErr := h.config.CursorCodec.Encode(collection, result.NextCursor, h.config.CursorCodec.now().Add(24*time.Hour))
		if encodeErr != nil {
			writeError(w, r, domainInternal())
			return
		}
		response["next_cursor"] = next
	}
	writeJSON(w, http.StatusOK, response)
}

// The remaining read handlers stay registered while their storage adapters are
// implemented so listener-isolation tests cover the complete surface.
func (h *Handler) adminGetAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("attachment_id")
	if !validResourceID(id) {
		writeError(w, r, validation("attachment_id", errors.New("invalid ID")))
		return
	}
	reader, info, err := h.config.Admin.OpenAttachment(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", info.MediaType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", safeFilename(info.Filename)))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}
func (h *Handler) adminListBans(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "active"
	}
	if state != "active" && state != "history" && state != "all" {
		writeError(w, r, validation("state", errors.New("invalid ban state")))
		return
	}
	collection := "admin:bans:" + state
	limit, cursor, ok := h.pagination(w, r, collection)
	if !ok {
		return
	}
	page, err := h.config.Admin.ListBans(r.Context(), state, cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if page.Items == nil {
		page.Items = []AdminBan{}
	}
	h.writePage(w, r, collection, "bans", page.Items, page.NextCursor)
}
func (h *Handler) adminListAudit(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := h.pagination(w, r, "admin:audit")
	if !ok {
		return
	}
	page, err := h.config.Admin.ListAudit(r.Context(), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if page.Items == nil {
		page.Items = []AdminAuditEntry{}
	}
	h.writePage(w, r, "admin:audit", "entries", page.Items, page.NextCursor)
}
func (h *Handler) adminCreatePostBan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("post_id")
	if !validResourceID(id) {
		writeError(w, r, validation("post_id", errors.New("invalid ID")))
		return
	}
	var body PostBanRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if !validReason(body.Reason) || !validPostBanOperations(body.Operations) || !body.ExpiresAt.After(timeNow()) {
		writeError(w, r, validation("body", errors.New("invalid post ban")))
		return
	}
	ban, err := h.config.Admin.CreatePostBan(r.Context(), id, body, RequestID(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ban)
}

func validPostBanOperations(operations []string) bool {
	if len(operations) < 1 || len(operations) > 4 {
		return false
	}
	allowed := map[string]bool{"create_thread": true, "create_post": true, "delete_post": true, "report": true}
	seen := make(map[string]bool, len(operations))
	for _, operation := range operations {
		if !allowed[operation] || seen[operation] {
			return false
		}
		seen[operation] = true
	}
	return true
}

func (h *Handler) adminPending(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, domain.NewError(domain.CodeServiceUnavailable, "", errors.New("administrative capability unavailable")))
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

func validReason(reason string) bool {
	return len(reason) >= 3 && len(reason) <= 500 && !security.ContainsRawIP(reason)
}

var timeNow = func() time.Time { return time.Now() }
