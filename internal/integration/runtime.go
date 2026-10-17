// Package integration wires the lane-local use cases to production adapters.
package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"bord/internal/api"
	"bord/internal/app/attachment"
	boardapp "bord/internal/app/board"
	"bord/internal/app/moderation"
	"bord/internal/domain"
	"bord/internal/media"
	"bord/internal/security"
	storage "bord/internal/storage/sqlite"
)

type Clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type Runtime struct {
	DB          *sql.DB
	Repo        *storage.Repository
	Board       *boardapp.Service
	Attachments attachment.Service
	Media       *media.Store
	Abuse       *Abuse
	Admin       *Admin
	Outbox      *Outbox
	UploadSlots chan struct{}
	IPKeyring   security.IPKeyring
}

type Config struct {
	DB                      *sql.DB
	Blobs                   string
	LookupKey               []byte
	EncryptionKey           []byte
	EncryptionKeyID         string
	PreviousEncryptionKey   []byte
	PreviousEncryptionKeyID string
	StoreRawIP              bool
	Clock                   Clock
	UploadConcurrency       int
}

func New(config Config) (*Runtime, error) {
	if config.DB == nil || config.Blobs == "" || len(config.LookupKey) < 32 {
		return nil, errors.New("integration: invalid runtime configuration")
	}
	if config.UploadConcurrency < 0 || config.UploadConcurrency > 16 {
		return nil, errors.New("integration: upload concurrency exceeds safety limit")
	}
	if config.UploadConcurrency == 0 {
		config.UploadConcurrency = 2
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	repo := storage.NewRepository(config.DB)
	blobs, err := media.NewStore(config.Blobs, media.SyncData)
	if err != nil {
		return nil, err
	}
	attachmentRepository := &attachmentRepo{repo: repo}
	attachments := attachment.Service{Repository: attachmentRepository, Blobs: media.AttachmentStore{Store: blobs}, Tx: repo, Clock: config.Clock, Random: rand.Reader}
	verifier := tokenVerifier{}
	board := boardapp.NewService(repo, repo, config.Clock, verifier)
	keyring := security.IPKeyring{}
	if config.StoreRawIP {
		if len(config.EncryptionKey) != 32 {
			_ = blobs.Close()
			return nil, errors.New("integration: IP encryption key must be 32 bytes")
		}
		if config.EncryptionKeyID == "" {
			config.EncryptionKeyID = "primary"
		}
		keyring = security.IPKeyring{Primary: config.EncryptionKeyID, Keys: map[string][]byte{config.EncryptionKeyID: append([]byte(nil), config.EncryptionKey...)}}
		if config.PreviousEncryptionKeyID != "" {
			if len(config.PreviousEncryptionKey) != 32 || config.PreviousEncryptionKeyID == config.EncryptionKeyID {
				_ = blobs.Close()
				return nil, errors.New("integration: invalid previous IP encryption key")
			}
			keyring.Keys[config.PreviousEncryptionKeyID] = append([]byte(nil), config.PreviousEncryptionKey...)
		}
	}
	limiter, err := security.NewLimiter(config.Clock, map[string]security.Rate{
		"read":   {Capacity: 60, RefillEvery: time.Second},
		"write":  {Capacity: 3, RefillEvery: 30 * time.Second},
		"report": {Capacity: 2, RefillEvery: time.Minute},
	})
	if err != nil {
		_ = blobs.Close()
		return nil, err
	}
	pow, err := security.NewPoW(config.LookupKey, config.Clock, rand.Reader, &challengeUses{db: config.DB}, 20)
	if err != nil {
		_ = blobs.Close()
		return nil, err
	}
	admin, err := newAdmin(config.DB, config.Clock, keyring, config.LookupKey)
	if err != nil {
		_ = blobs.Close()
		return nil, err
	}
	r := &Runtime{DB: config.DB, Repo: repo, Board: board, Attachments: attachments, Media: blobs, Admin: admin, Outbox: &Outbox{db: config.DB}, IPKeyring: keyring}
	r.UploadSlots = make(chan struct{}, config.UploadConcurrency)
	r.Abuse = &Abuse{db: config.DB, clock: config.Clock, lookupKey: append([]byte(nil), config.LookupKey...), keyring: keyring, storeRawIP: config.StoreRawIP, limiter: limiter, pow: pow, pending: make(map[string]int64)}
	return r, nil
}

func (r *Runtime) Close() error { return errors.Join(r.Media.Close(), r.DB.Close()) }
func (r *Runtime) Ready(ctx context.Context) error {
	if err := storage.Ready(ctx, r.DB); err != nil {
		return err
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT DISTINCT key_id FROM request_subjects WHERE encrypted_ip IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if len(r.IPKeyring.Keys[id]) != 32 {
			return fmt.Errorf("encrypted IP data references unavailable key id %q", id)
		}
	}
	return rows.Err()
}

// Maintain applies privacy retention and reconciles old unreferenced blobs.
// Callers run it before readiness and periodically for automatic cleanup.
type MaintenanceResult struct {
	Retention moderation.RetentionResult
	Media     attachment.CleanupReport
}

func (r *Runtime) Maintain(ctx context.Context) (MaintenanceResult, error) {
	var result MaintenanceResult
	retention, err := r.Abuse.Retain(ctx)
	if err != nil {
		return result, err
	}
	result.Retention = retention
	reconciler := attachment.Reconciler{
		Repository: r.Attachments.Repository,
		Cleaner:    media.AttachmentStore{Store: r.Media},
		Clock:      r.Abuse.clock,
		Grace:      24 * time.Hour,
	}
	result.Media, err = reconciler.Run(ctx, false)
	return result, err
}

func (r *Runtime) SeedBoard(ctx context.Context, slug, title string) error {
	var count int
	if err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM boards WHERE slug = ?`, slug).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	id, err := r.Repo.NextID(ctx, "board")
	if err != nil {
		return err
	}
	b, err := domain.NewBoard(domain.BoardID(id), slug, title)
	if err != nil {
		return err
	}
	return r.Repo.CreateBoard(ctx, b, r.Abuse.clock.Now().UTC())
}

type tokenVerifier struct{}

func (tokenVerifier) Verify(token, encoded []byte) (bool, error) {
	return security.VerifySecret(token, string(encoded)), nil
}

// Public adapts board, report, and subject services to the frozen HTTP port.
type Public struct{ Runtime *Runtime }

func (p Public) ListBoards(ctx context.Context) ([]api.Board, error) {
	items, err := p.Runtime.Board.Boards(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Board, 0, len(items))
	for _, b := range items {
		out = append(out, api.Board{ID: string(b.ID), Slug: b.Slug, Title: b.Title})
	}
	return out, nil
}
func (p Public) ListThreads(ctx context.Context, slug, cursor string, limit int) (api.Page[api.Thread], error) {
	var c *boardapp.CatalogCursor
	if cursor != "" {
		parts := strings.Split(cursor, "|")
		if len(parts) != 2 {
			return api.Page[api.Thread]{}, domain.NewError(domain.CodeValidation, "cursor", nil)
		}
		at, e := time.Parse(time.RFC3339Nano, parts[0])
		if e != nil {
			return api.Page[api.Thread]{}, domain.NewError(domain.CodeValidation, "cursor", e)
		}
		c = &boardapp.CatalogCursor{BumpedAt: at, ThreadID: domain.ThreadID(parts[1])}
	}
	page, err := p.Runtime.Board.Catalog(ctx, slug, c, limit)
	if err != nil {
		return api.Page[api.Thread]{}, err
	}
	out := api.Page[api.Thread]{Items: make([]api.Thread, 0, len(page.Threads))}
	for _, t := range page.Threads {
		out.Items = append(out.Items, mapThread(t, nil))
	}
	if page.Next != nil {
		out.NextCursor = page.Next.BumpedAt.Format(time.RFC3339Nano) + "|" + string(page.Next.ThreadID)
	}
	return out, nil
}
func (p Public) GetThread(ctx context.Context, id string) (api.Thread, error) {
	t, e := p.Runtime.Repo.ThreadByID(ctx, domain.ThreadID(id))
	if e != nil {
		return api.Thread{}, e
	}
	posts, e := p.Runtime.Board.Posts(ctx, t.ID, "", 1)
	if e != nil {
		return api.Thread{}, e
	}
	var first *api.Post
	if len(posts.Posts) > 0 {
		x := p.mapPost(ctx, posts.Posts[0])
		first = &x
	}
	return mapThread(t, first), nil
}
func (p Public) ListPosts(ctx context.Context, id, cursor string, limit int) (api.Page[api.Post], error) {
	page, e := p.Runtime.Board.Posts(ctx, domain.ThreadID(id), domain.PostID(cursor), limit)
	if e != nil {
		return api.Page[api.Post]{}, e
	}
	out := api.Page[api.Post]{Items: make([]api.Post, 0, len(page.Posts)), NextCursor: string(page.Next)}
	for _, v := range page.Posts {
		out.Items = append(out.Items, p.mapPost(ctx, v))
	}
	return out, nil
}

func author(proof, thread string) domain.AuthorID {
	sum := sha256.Sum256([]byte("bord/http-session/v1\x00" + proof + "\x00" + thread))
	return domain.AuthorID(hex.EncodeToString(sum[:12]))
}
func decodeVerifier(value *string) ([]byte, error) {
	if value == nil {
		return nil, domain.NewError(domain.CodeValidation, "delete_verifier", nil)
	}
	raw, err := base64.RawURLEncoding.DecodeString(*value)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "delete_verifier", err)
	}
	return raw, nil
}
func body(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func attachmentID(v *string) domain.AttachmentID {
	if v == nil {
		return ""
	}
	return domain.AttachmentID(*v)
}
func (p Public) CreateThread(ctx context.Context, slug, key string, c api.CreateContent) (api.CreateResult, error) {
	defer p.Runtime.Abuse.discard(ctx)
	v, e := decodeVerifier(c.DeleteVerifier)
	if e != nil {
		return api.CreateResult{}, e
	}
	res, e := p.Runtime.Board.CreateThread(ctx, boardapp.CreateThreadInput{BoardSlug: slug, Body: body(c.Body), AttachmentID: attachmentID(c.AttachmentID), AuthorID: author(value(c.SessionProof), slug), DeleteVerifier: v, IdempotencyKey: key})
	if e != nil {
		return api.CreateResult{}, e
	}
	p.Runtime.Abuse.bindPost(ctx, string(res.Post.ID))
	return api.CreateResult{ThreadID: string(res.Thread.ID), PostID: string(res.Post.ID)}, nil
}
func (p Public) CreatePost(ctx context.Context, id, key string, c api.CreateContent) (api.CreateResult, error) {
	defer p.Runtime.Abuse.discard(ctx)
	v, e := decodeVerifier(c.DeleteVerifier)
	if e != nil {
		return api.CreateResult{}, e
	}
	var reply domain.PostID
	if c.ReplyToID != nil {
		reply = domain.PostID(*c.ReplyToID)
	}
	res, e := p.Runtime.Board.CreateReply(ctx, boardapp.CreateReplyInput{ThreadID: domain.ThreadID(id), ReplyToID: reply, Body: body(c.Body), AttachmentID: attachmentID(c.AttachmentID), AuthorID: author(value(c.SessionProof), id), DeleteVerifier: v, IdempotencyKey: key})
	if e != nil {
		return api.CreateResult{}, e
	}
	p.Runtime.Abuse.bindPost(ctx, string(res.Post.ID))
	return api.CreateResult{ThreadID: id, PostID: string(res.Post.ID)}, nil
}
func value(v *string) string {
	if v == nil {
		return "anonymous"
	}
	return *v
}
func (p Public) DeletePost(ctx context.Context, id, token string) error {
	return p.Runtime.Board.DeleteOwnPost(ctx, domain.PostID(id), []byte(token))
}
func (p Public) ReportPost(ctx context.Context, id string, request api.ReportRequest) error {
	sid := p.Runtime.Abuse.subjectID(ctx)
	if sid == 0 {
		return domain.NewError(domain.CodeInternal, "", nil)
	}
	var exists int
	if err := p.Runtime.DB.QueryRowContext(ctx, `SELECT count(*) FROM posts WHERE id=? AND status='visible'`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return domain.NewError(domain.CodePostNotFound, "post_id", nil)
	}
	_, err := p.Runtime.DB.ExecContext(ctx, `INSERT OR IGNORE INTO reports(post_id,subject_id,reason,created_at) VALUES(?,?,?,?)`, id, sid, request.Reason, encodeTime(p.Runtime.Abuse.clock.Now()))
	return err
}

func mapThread(t domain.Thread, first *api.Post) api.Thread {
	return api.Thread{ID: string(t.ID), BoardID: string(t.BoardID), CreatedAt: t.CreatedAt, UpdatedAt: t.BumpedAt, Closed: t.Status != domain.ThreadOpen, InitialPost: first}
}
func mapPost(p domain.Post) api.Post {
	out := api.Post{ID: string(p.ID), ThreadID: string(p.ThreadID), AuthorID: string(p.AuthorID), State: string(p.Status), CreatedAt: p.CreatedAt, DeletedAt: p.DeletedAt}
	if p.Status == domain.PostVisible {
		b := p.Body
		out.Body = &b
		if p.ReplyToID.Valid() {
			x := string(p.ReplyToID)
			out.ReplyToID = &x
		}
	}
	return out
}

func (p Public) mapPost(ctx context.Context, post domain.Post) api.Post {
	out := mapPost(post)
	if post.Status == domain.PostVisible && post.AttachmentID.Valid() {
		if record, err := p.Runtime.Attachments.Repository.ByID(ctx, post.AttachmentID); err == nil {
			attachment := attachmentDTO(record)
			out.Attachment = &attachment
		}
	}
	return out
}

type Media struct {
	Runtime   *Runtime
	Processor media.Processor
}

func (m Media) Upload(ctx context.Context, contentType string, r io.Reader) (api.Attachment, error) {
	select {
	case m.Runtime.UploadSlots <- struct{}{}:
		defer func() { <-m.Runtime.UploadSlots }()
	case <-ctx.Done():
		return api.Attachment{}, ctx.Err()
	}
	processor := m.Processor
	if processor.Limits.MaxBytes == 0 {
		processor.Limits = media.DefaultLimits()
	}
	img, err := processor.Normalize(ctx, r, "image")
	if err != nil {
		return api.Attachment{}, mapMediaError(err)
	}
	record, err := m.Runtime.Attachments.Prepare(ctx, attachment.Input{Data: img.Data, MediaType: img.MediaType, Filename: img.Filename, Width: img.Width, Height: img.Height, SHA256: img.SHA256})
	if err != nil {
		return api.Attachment{}, err
	}
	return attachmentDTO(record), nil
}
func (m Media) Open(ctx context.Context, id string) (io.ReadCloser, api.Attachment, error) {
	d, err := m.Runtime.Attachments.Fetch(ctx, domain.AttachmentID(id))
	if err != nil {
		return nil, api.Attachment{}, mapAttachmentError(err)
	}
	return d.Body, attachmentDTO(d.Record), nil
}
func attachmentDTO(r attachment.Record) api.Attachment {
	return api.Attachment{ID: string(r.ID), MediaType: r.MediaType, Filename: r.Filename, Size: r.Size, Width: r.Width, Height: r.Height, SHA256: hex.EncodeToString(r.SHA256[:])}
}
func mapMediaError(err error) error {
	if errors.Is(err, media.ErrTooLarge) {
		return domain.NewError(domain.CodePayloadTooLarge, "", err)
	}
	return domain.NewError(domain.CodeValidation, "attachment", err)
}
func mapAttachmentError(err error) error {
	if errors.Is(err, attachment.ErrNotFound) || errors.Is(err, media.ErrBlobNotFound) {
		return domain.NewError(domain.CodeAttachmentNotFound, "attachment_id", err)
	}
	return err
}

type attachmentRepo struct{ repo *storage.Repository }

func (r *attachmentRepo) CreatePrepared(ctx context.Context, a attachment.Record) error {
	_, e := r.repo.ExecContext(ctx, `INSERT INTO attachments(id,state,media_type,size_bytes,sha256,width,height,safe_filename,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, a.ID, a.State, a.MediaType, a.Size, a.SHA256[:], a.Width, a.Height, a.Filename, encodeTime(a.CreatedAt))
	return e
}
func (r *attachmentRepo) ByID(ctx context.Context, id domain.AttachmentID) (attachment.Record, error) {
	return r.get(ctx, id, false)
}
func (r *attachmentRepo) PublicByID(ctx context.Context, id domain.AttachmentID) (attachment.Record, error) {
	return r.get(ctx, id, true)
}
func (r *attachmentRepo) get(ctx context.Context, id domain.AttachmentID, public bool) (attachment.Record, error) {
	q := `SELECT a.id,a.state,a.media_type,a.size_bytes,a.sha256,a.width,a.height,a.safe_filename,a.created_at,coalesce(a.deleted_at,'') FROM attachments a`
	if public {
		q += ` JOIN posts p ON p.attachment_id=a.id AND p.status='visible' WHERE a.id=? AND a.state='attached'`
	} else {
		q += ` WHERE a.id=?`
	}
	var a attachment.Record
	var hash []byte
	var created, deleted string
	e := r.repo.QueryRowContext(ctx, q, id).Scan(&a.ID, &a.State, &a.MediaType, &a.Size, &hash, &a.Width, &a.Height, &a.Filename, &created, &deleted)
	if errors.Is(e, sql.ErrNoRows) {
		return a, attachment.ErrNotFound
	}
	if e != nil {
		return a, e
	}
	copy(a.SHA256[:], hash)
	a.BlobKey = hex.EncodeToString(hash)
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if deleted != "" {
		a.DeletedAt, _ = time.Parse(time.RFC3339Nano, deleted)
	}
	return a, nil
}
func (r *attachmentRepo) Attach(ctx context.Context, id domain.AttachmentID, post domain.PostID) (attachment.Record, error) {
	result, e := r.repo.ExecContext(ctx, `UPDATE attachments SET state='attached',attached_at=? WHERE id=? AND state='prepared' AND EXISTS (SELECT 1 FROM posts WHERE id=? AND attachment_id=attachments.id)`, encodeTime(time.Now()), id, post)
	if e != nil {
		return attachment.Record{}, e
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return attachment.Record{}, attachment.ErrInvalidState
	}
	return r.ByID(ctx, id)
}
func (r *attachmentRepo) MarkDeleted(ctx context.Context, id domain.AttachmentID, at time.Time) (attachment.Record, error) {
	result, e := r.repo.ExecContext(ctx, `UPDATE attachments SET state='deleted',deleted_at=? WHERE id=? AND state!='deleted'`, encodeTime(at), id)
	if e != nil {
		return attachment.Record{}, e
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return attachment.Record{}, attachment.ErrNotFound
	}
	return r.ByID(ctx, id)
}
func (r *attachmentRepo) ReferencedBlobKeys(ctx context.Context) (map[string]struct{}, error) {
	rows, e := r.repo.DB().QueryContext(ctx, `SELECT sha256 FROM attachments WHERE state!='deleted'`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out[hex.EncodeToString(b)] = struct{}{}
	}
	return out, rows.Err()
}

func encodeTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
