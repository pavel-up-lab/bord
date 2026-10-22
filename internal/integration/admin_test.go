package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"bord/internal/api"
	"bord/internal/domain"
	"bord/internal/media"
	"bord/internal/security"
	storage "bord/internal/storage/sqlite"
)

func TestAdminActorEnforcesPrincipalRole(t *testing.T) {
	admin := &Admin{}
	tests := []struct {
		name      string
		role      security.Role
		required  security.Role
		wantError domain.Code
	}{
		{name: "reports reads reports", role: security.RoleReports, required: security.RoleReports},
		{name: "reports cannot moderate", role: security.RoleReports, required: security.RoleModerator, wantError: domain.CodeForbidden},
		{name: "moderator moderates", role: security.RoleModerator, required: security.RoleModerator},
		{name: "moderator cannot reveal", role: security.RoleModerator, required: security.RoleAdministrator, wantError: domain.CodeForbidden},
		{name: "administrator reveals", role: security.RoleAdministrator, required: security.RoleAdministrator},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := api.WithAdminPrincipal(context.Background(), api.AdminPrincipal{ID: "actor", Role: test.role})
			actor, err := admin.actor(ctx, test.required)
			if test.wantError != "" {
				if domain.ErrorCode(err) != test.wantError {
					t.Fatalf("error=%v code=%q want=%q", err, domain.ErrorCode(err), test.wantError)
				}
				return
			}
			if err != nil || actor.ID != "actor" {
				t.Fatalf("actor=%#v err=%v", actor, err)
			}
		})
	}
}

func TestAdminActorIsolatedAcrossParallelContexts(t *testing.T) {
	admin := &Admin{}
	const workers = 64
	var group sync.WaitGroup
	errors := make(chan error, workers)
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			id := fmt.Sprintf("actor-%d", index)
			ctx := api.WithAdminPrincipal(context.Background(), api.AdminPrincipal{ID: id, Role: security.RoleAdministrator})
			actor, err := admin.actor(ctx, security.RoleModerator)
			if err != nil || actor.ID != id {
				errors <- fmt.Errorf("id=%s actor=%s err=%w", id, actor.ID, err)
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestAdminActorRejectsMissingPrincipal(t *testing.T) {
	_, err := (&Admin{}).actor(context.Background(), security.RoleReports)
	if domain.ErrorCode(err) != domain.CodeUnauthorized {
		t.Fatalf("error=%v code=%q", err, domain.ErrorCode(err))
	}
}

func TestAdminPostContextIncludesVisibleAndHiddenContent(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir() + "/bord.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	runtime, err := New(Config{DB: db, Blobs: t.TempDir(), LookupKey: bytes.Repeat([]byte{1}, 32), EncryptionKey: bytes.Repeat([]byte{2}, 32), StoreRawIP: true, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	at := encodeTime(clock.Now())
	if _, err := db.Exec(`INSERT INTO boards(id,slug,title,created_at) VALUES(1,'general','General',?)`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO admin_identities(id,role,credential_verifier,created_at,expires_at) VALUES
		('mod','moderator',X'00',?,?),('reader','reports',X'00',?,?)`, at, encodeTime(clock.Now().Add(time.Hour)), at, encodeTime(clock.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO threads(id,board_id,status,created_at,bumped_at) VALUES(1,1,'open',?,?)`, at, at); err != nil {
		t.Fatal(err)
	}
	imageData := pngFixture(t)
	imageHash := sha256.Sum256(imageData)
	if _, err := runtime.Media.Put(ctx, media.Image{Data: imageData, MediaType: "image/png", Filename: "safe.png", Width: 1, Height: 1, SHA256: imageHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO attachments(id,state,media_type,size_bytes,sha256,width,height,safe_filename,created_at,attached_at) VALUES('attach_1','attached','image/png',?,?,1,1,'safe.png',?,?)`, len(imageData), imageHash[:], at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO posts(id,thread_id,author_id,body,status,created_at) VALUES(1,1,'author01','visible body','visible',?)`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO posts(id,thread_id,reply_to_id,author_id,body,status,created_at,hidden_body,hidden_attachment_id) VALUES(2,1,1,'author02','','hidden',?,'hidden body','attach_1')`, at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO posts(id,thread_id,author_id,body,status,created_at,deleted_at) VALUES(3,1,'author03','','deleted',?,?)`, at, at); err != nil {
		t.Fatal(err)
	}
	moderator := api.WithAdminPrincipal(ctx, api.AdminPrincipal{ID: "mod", Role: security.RoleModerator, ExpiresAt: clock.Now().Add(time.Hour)})
	page, err := runtime.Admin.GetPostContext(moderator, "00000002", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.Board.Slug != "general" || page.Thread.ID != "00000001" || page.TargetPost != "00000002" || len(page.Posts) != 2 || page.NextCursor == "" {
		t.Fatalf("unexpected context: %#v", page)
	}
	hidden := page.Posts[1]
	if hidden.State != "hidden" || hidden.Body == nil || *hidden.Body != "hidden body" || hidden.Attachment == nil || hidden.Attachment.ID != "attach_1" || hidden.Attachment.SHA256 != hex.EncodeToString(imageHash[:]) {
		t.Fatalf("hidden content not restored for moderation: %#v", hidden)
	}
	reader, attachment, err := runtime.Admin.OpenAttachment(moderator, "attach_1")
	if err != nil {
		t.Fatal(err)
	}
	gotImage, _ := io.ReadAll(reader)
	_ = reader.Close()
	if !bytes.Equal(gotImage, imageData) || attachment.SHA256 != hex.EncodeToString(imageHash[:]) {
		t.Fatalf("admin attachment mismatch: %#v", attachment)
	}
	if _, err := db.Exec(`INSERT INTO attachments(id,state,media_type,size_bytes,sha256,width,height,safe_filename,created_at) VALUES('orphan_1','prepared','image/png',?,?,1,1,'orphan.png',?)`, len(imageData), imageHash[:], at); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.Admin.OpenAttachment(moderator, "orphan_1"); domain.ErrorCode(err) != domain.CodeAttachmentNotFound {
		t.Fatalf("unreferenced attachment error=%v code=%q", err, domain.ErrorCode(err))
	}
	next, err := runtime.Admin.GetPostContext(moderator, "00000002", page.NextCursor, 2)
	if err != nil || len(next.Posts) != 1 || next.Posts[0].State != "deleted" || next.Posts[0].Body != nil {
		t.Fatalf("next context=%#v err=%v", next, err)
	}
	if _, err := runtime.Admin.GetPostContext(moderator, "99999999", "", 2); domain.ErrorCode(err) != domain.CodePostNotFound {
		t.Fatalf("missing post error=%v code=%q", err, domain.ErrorCode(err))
	}
	reporter := api.WithAdminPrincipal(ctx, api.AdminPrincipal{ID: "reader", Role: security.RoleReports, ExpiresAt: clock.Now().Add(time.Hour)})
	if _, err := runtime.Admin.GetPostContext(reporter, "00000002", "", 2); domain.ErrorCode(err) != domain.CodeForbidden {
		t.Fatalf("reports role context error=%v code=%q", err, domain.ErrorCode(err))
	}
	if _, err := db.Exec(`INSERT INTO bans(id,subject_kind,subject_value,operation,reason,created_at,expires_at,revoked_at) VALUES
		(1,'lookup',?,'create_post','active reason',?,?,NULL),
		(2,'lookup',?,'report','expired reason',?,?,NULL),
		(3,'prefix',?,'create_thread','revoked reason',?,?,?)`,
		[]byte("raw-active-subject"), at, encodeTime(clock.Now().Add(time.Hour)),
		[]byte("raw-expired-subject"), at, encodeTime(clock.Now().Add(-time.Hour)),
		[]byte("198.51.100.0/24"), at, encodeTime(clock.Now().Add(time.Hour)), at); err != nil {
		t.Fatal(err)
	}
	active, err := runtime.Admin.ListBans(moderator, "active", "", 10)
	if err != nil || len(active.Items) != 1 || active.Items[0].State != "active" || strings.Contains(active.Items[0].Subject, "raw-active-subject") {
		t.Fatalf("active bans=%#v err=%v", active, err)
	}
	history, err := runtime.Admin.ListBans(moderator, "history", "", 1)
	if err != nil || len(history.Items) != 1 || history.Items[0].State != "revoked" || history.NextCursor == "" || strings.Contains(history.Items[0].Subject, "198.51.100") {
		t.Fatalf("history page=%#v err=%v", history, err)
	}
	historyNext, err := runtime.Admin.ListBans(moderator, "history", history.NextCursor, 1)
	if err != nil || len(historyNext.Items) != 1 || historyNext.Items[0].State != "expired" {
		t.Fatalf("history next=%#v err=%v", historyNext, err)
	}
	if _, err := runtime.Admin.ListBans(reporter, "all", "", 10); domain.ErrorCode(err) != domain.CodeForbidden {
		t.Fatalf("reports role bans error=%v code=%q", err, domain.ErrorCode(err))
	}
	address := netip.MustParseAddr("203.0.113.7")
	lookup, err := security.IPLookup(bytes.Repeat([]byte{1}, 32), address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO request_subjects(ip_lookup,created_at,expires_at,post_id) VALUES(?,?,?,2)`, lookup[:], at, encodeTime(clock.Now().Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	createdBan, err := runtime.Admin.CreatePostBan(moderator, "00000002", api.PostBanRequest{Operations: []string{"create_post"}, ExpiresAt: clock.Now().Add(2 * time.Hour), Reason: "repeated abuse"}, "request-ban-001")
	if err != nil || createdBan.ID == "" {
		t.Fatalf("create post ban=%#v err=%v", createdBan, err)
	}
	bans, err := runtime.Abuse.bans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, blocked := security.EvaluateBans(bans, security.BanSubject{IP: address, Lookup: lookup[:], Operation: "create_post"}, clock.Now()); !blocked {
		t.Fatal("post lookup ban was not applied")
	}
	if _, err := runtime.Admin.CreatePostBan(moderator, "00000002", api.PostBanRequest{Operations: []string{"create_post"}, ExpiresAt: clock.Now(), Reason: "expired ban"}, "request-ban-002"); domain.ErrorCode(err) != domain.CodeValidation {
		t.Fatalf("expired post ban error=%v code=%q", err, domain.ErrorCode(err))
	}
	if _, err := runtime.Admin.CreatePostBan(reporter, "00000002", api.PostBanRequest{Operations: []string{"create_post"}, ExpiresAt: clock.Now().Add(time.Hour), Reason: "denied ban"}, "request-ban-003"); domain.ErrorCode(err) != domain.CodeForbidden {
		t.Fatalf("reports role post ban error=%v code=%q", err, domain.ErrorCode(err))
	}
	if _, err := runtime.Admin.CreatePostBan(moderator, "00000002", api.PostBanRequest{Operations: []string{"unsupported"}, ExpiresAt: clock.Now().Add(time.Hour), Reason: "invalid operation"}, "request-ban-004"); domain.ErrorCode(err) != domain.CodeValidation {
		t.Fatalf("invalid operation error=%v code=%q", err, domain.ErrorCode(err))
	}
	var successfulBans int
	if err := db.QueryRow(`SELECT count(*) FROM moderation_actions WHERE action='ban.create' AND outcome='success' AND request_id='request-ban-001'`).Scan(&successfulBans); err != nil || successfulBans != 1 {
		t.Fatalf("post ban audit count=%d err=%v", successfulBans, err)
	}
	if _, err := db.Exec(`INSERT INTO moderation_actions(action,target_id,reason,outcome,request_id,payload,created_at) VALUES
		('thread.close','00000001','routine moderation','success','request-001','{}',?),
		('post.visibility','00000002','source 192.0.2.10','failure','request-002','{"error_code":"forbidden","credential":"top-secret"}',?)`, at, at); err != nil {
		t.Fatal(err)
	}
	audit, err := runtime.Admin.ListAudit(moderator, "", 1)
	if err != nil || len(audit.Items) != 1 || audit.Items[0].Outcome != "failure" || audit.Items[0].ErrorCode != "forbidden" || audit.Items[0].Reason != "[redacted: raw IP address]" || audit.NextCursor == "" {
		t.Fatalf("audit page=%#v err=%v", audit, err)
	}
	auditNext, err := runtime.Admin.ListAudit(moderator, audit.NextCursor, 1)
	if err != nil || len(auditNext.Items) != 1 || auditNext.Items[0].Outcome != "success" {
		t.Fatalf("audit next=%#v err=%v", auditNext, err)
	}
	if _, err := runtime.Admin.ListAudit(reporter, "", 10); domain.ErrorCode(err) != domain.CodeForbidden {
		t.Fatalf("reports role audit error=%v code=%q", err, domain.ErrorCode(err))
	}
}
