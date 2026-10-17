package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"bord/internal/domain"
)

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	for _, probe := range h.config.Readiness {
		if probe == nil || probe.Ready(r.Context()) != nil {
			writeError(w, r, unavailable())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listBoards(w http.ResponseWriter, r *http.Request) {
	if !h.checkRead(w, r, "list_boards") {
		return
	}
	if h.config.Public == nil {
		writeError(w, r, unavailable())
		return
	}
	boards, err := h.config.Public.ListBoards(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	if boards == nil {
		boards = []Board{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
}

func (h *Handler) listThreads(w http.ResponseWriter, r *http.Request) {
	if !validBoardSlug(r.PathValue("slug")) {
		writeError(w, r, validation("slug", errors.New("invalid board slug")))
		return
	}
	if h.config.Public == nil {
		writeError(w, r, unavailable())
		return
	}
	if !h.checkRead(w, r, "list_threads") {
		return
	}
	limit, cursor, ok := h.pagination(w, r, "threads:"+r.PathValue("slug"))
	if !ok {
		return
	}
	page, err := h.config.Public.ListThreads(r.Context(), r.PathValue("slug"), cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if page.Items == nil {
		page.Items = []Thread{}
	}
	h.writePage(w, r, "threads:"+r.PathValue("slug"), "threads", page.Items, page.NextCursor)
}

func (h *Handler) getThread(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("thread_id")
	if !validResourceID(id) {
		writeError(w, r, validation("thread_id", errors.New("invalid ID")))
		return
	}
	if h.config.Public == nil {
		writeError(w, r, unavailable())
		return
	}
	if !h.checkRead(w, r, "get_thread") {
		return
	}
	thread, err := h.config.Public.GetThread(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (h *Handler) listPosts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("thread_id")
	if !validResourceID(id) {
		writeError(w, r, validation("thread_id", errors.New("invalid ID")))
		return
	}
	if h.config.Public == nil {
		writeError(w, r, unavailable())
		return
	}
	if !h.checkRead(w, r, "list_posts") {
		return
	}
	limit, cursor, ok := h.pagination(w, r, "posts:"+id)
	if !ok {
		return
	}
	page, err := h.config.Public.ListPosts(r.Context(), id, cursor, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if page.Items == nil {
		page.Items = []Post{}
	}
	h.writePage(w, r, "posts:"+id, "posts", page.Items, page.NextCursor)
}

func (h *Handler) createThread(w http.ResponseWriter, r *http.Request) {
	if !validBoardSlug(r.PathValue("slug")) {
		writeError(w, r, validation("slug", errors.New("invalid board slug")))
		return
	}
	h.create(w, r, "create_thread", false, func(key string, content CreateContent) (CreateResult, error) {
		return h.config.Public.CreateThread(r.Context(), r.PathValue("slug"), key, content)
	})
}

func (h *Handler) createPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("thread_id")
	if !validResourceID(id) {
		writeError(w, r, validation("thread_id", errors.New("invalid ID")))
		return
	}
	h.create(w, r, "create_post", true, func(key string, content CreateContent) (CreateResult, error) {
		return h.config.Public.CreatePost(r.Context(), id, key, content)
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, operation string, allowReply bool, call func(string, CreateContent) (CreateResult, error)) {
	if h.config.Public == nil {
		writeError(w, r, unavailable())
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(w, r, validation("Idempotency-Key", errors.New("invalid key")))
		return
	}
	var content CreateContent
	if err := decodeJSON(w, r, &content); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if (content.Body == nil || strings.TrimSpace(*content.Body) == "") && content.AttachmentID == nil {
		writeError(w, r, validation("body", errors.New("body or attachment is required")))
		return
	}
	if content.Body != nil && utf8.RuneCountInString(*content.Body) > 10000 {
		writeError(w, r, validation("body", errors.New("too long")))
		return
	}
	if content.AttachmentID != nil && !validResourceID(*content.AttachmentID) {
		writeError(w, r, validation("attachment_id", errors.New("invalid ID")))
		return
	}
	if content.SessionProof != nil && !validBoundedString(*content.SessionProof, 32, 512) {
		writeError(w, r, validation("session_proof", errors.New("invalid proof")))
		return
	}
	if content.DeleteVerifier != nil && !validBoundedString(*content.DeleteVerifier, 32, 512) {
		writeError(w, r, validation("delete_verifier", errors.New("invalid verifier")))
		return
	}
	if content.PoWSolution != nil && utf8.RuneCountInString(*content.PoWSolution) > 512 {
		writeError(w, r, validation("pow_solution", errors.New("solution too long")))
		return
	}
	if content.ReplyToID != nil && (!allowReply || !validResourceID(*content.ReplyToID)) {
		writeError(w, r, validation("reply_to_id", errors.New("invalid reply target")))
		return
	}
	if h.config.Abuse != nil {
		solution := r.Header.Get("X-PoW-Solution")
		if content.PoWSolution != nil {
			solution = *content.PoWSolution
		}
		var err error
		if extended, ok := h.config.Abuse.(interface {
			CheckSession(context.Context, string, string, string) error
		}); ok {
			err = extended.CheckSession(r.Context(), operation, solution, valueOrEmpty(content.SessionProof))
		} else {
			err = h.config.Abuse.Check(r.Context(), operation, solution)
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	result, err := call(key, content)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func validBoundedString(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func (h *Handler) deletePost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("post_id")
	if !validResourceID(id) {
		writeError(w, r, validation("post_id", errors.New("invalid ID")))
		return
	}
	var body struct {
		DeleteToken string `json:"delete_token"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if len(body.DeleteToken) < 32 || len(body.DeleteToken) > 512 {
		writeError(w, r, validation("delete_token", errors.New("invalid token")))
		return
	}
	if h.config.Public == nil {
		writeError(w, r, unavailable())
		return
	}
	if h.config.Abuse != nil {
		if err := h.config.Abuse.Check(r.Context(), "delete_post", ""); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if err := h.config.Public.DeletePost(r.Context(), id, body.DeleteToken); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reportPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("post_id")
	if !validResourceID(id) {
		writeError(w, r, validation("post_id", errors.New("invalid ID")))
		return
	}
	var body ReportRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	validReason := map[string]bool{"spam": true, "illegal": true, "harassment": true, "personal_data": true, "other": true}
	if !validReason[body.Reason] || body.Detail != nil && len(*body.Detail) > 500 {
		writeError(w, r, validation("reason", errors.New("invalid report")))
		return
	}
	if h.config.Public == nil {
		writeError(w, r, unavailable())
		return
	}
	if h.config.Abuse != nil {
		if err := h.config.Abuse.Check(r.Context(), "report", r.Header.Get("X-PoW-Solution")); err != nil {
			writeError(w, r, err)
			return
		}
	}
	if err := h.config.Public.ReportPost(r.Context(), id, body); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	if h.config.Media == nil {
		writeError(w, r, unavailable())
		return
	}
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "image/jpeg" && contentType != "image/png" {
		writeError(w, r, validation("Content-Type", errors.New("unsupported media")))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.config.MaxMediaBytes)
	attachment, err := h.config.Media.Upload(r.Context(), contentType, r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			err = domainErrorPayloadTooLarge(err)
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, attachment)
}

func (h *Handler) getAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("attachment_id")
	if !validResourceID(id) {
		writeError(w, r, validation("attachment_id", errors.New("invalid ID")))
		return
	}
	if h.config.Media == nil {
		writeError(w, r, unavailable())
		return
	}
	if !h.checkRead(w, r, "get_attachment") {
		return
	}
	reader, info, err := h.config.Media.Open(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer reader.Close()
	filename := safeFilename(info.Filename)
	w.Header().Set("Content-Type", info.MediaType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// checkRead is a frozen-port-safe extension: existing AbuseService adapters
// retain their behavior, while production can add bounded read dimensions
// without changing the public service interface or OpenAPI contract.
func (h *Handler) checkRead(w http.ResponseWriter, r *http.Request, operation string) bool {
	limiter, ok := h.config.Abuse.(interface {
		CheckRead(context.Context, string) error
	})
	if !ok {
		return true
	}
	if err := limiter.CheckRead(r.Context(), operation); err != nil {
		writeError(w, r, err)
		return false
	}
	return true
}

func (h *Handler) createChallenge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Purpose string `json:"purpose"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if body.Purpose != "create_thread" && body.Purpose != "create_post" && body.Purpose != "report" {
		writeError(w, r, validation("purpose", errors.New("invalid purpose")))
		return
	}
	if h.config.Abuse == nil {
		writeError(w, r, unavailable())
		return
	}
	challenge, err := h.config.Abuse.Challenge(r.Context(), body.Purpose)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, challenge)
}

func (h *Handler) pagination(w http.ResponseWriter, r *http.Request, collection string) (int, string, bool) {
	limit, err := pageLimit(r)
	if err != nil {
		writeError(w, r, validation("limit", err))
		return 0, "", false
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" {
		if h.config.CursorCodec == nil {
			writeError(w, r, validation("cursor", errors.New("cursor unsupported")))
			return 0, "", false
		}
		cursor, err = h.config.CursorCodec.Decode(cursor, collection)
		if err != nil {
			writeError(w, r, validation("cursor", err))
			return 0, "", false
		}
	}
	return limit, cursor, true
}

func (h *Handler) writePage(w http.ResponseWriter, r *http.Request, collection, field string, items any, next string) {
	response := map[string]any{field: items}
	if next != "" && h.config.CursorCodec != nil {
		encoded, err := h.config.CursorCodec.Encode(collection, next, h.config.CursorCodec.now().Add(24*time.Hour))
		if err != nil {
			writeError(w, r, domainInternal())
			return
		}
		response["next_cursor"] = encoded
	}
	writeJSON(w, http.StatusOK, response)
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if domain.ErrorCode(err) == domain.CodePayloadTooLarge {
		writeError(w, r, err)
	} else {
		writeError(w, r, validation("body", err))
	}
}

func safeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '\\' || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "attachment"
	}
	if len(name) > 200 {
		return name[:200]
	}
	return name
}

func validBoardSlug(slug string) bool {
	if len(slug) < 1 || len(slug) > 32 || slug[0] == '-' {
		return false
	}
	for _, c := range slug {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
