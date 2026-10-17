package attachment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	"bord/internal/domain"
)

func TestPrepareAttachFetchDeleteLifecycle(t *testing.T) {
	repo := newFakeRepository()
	blobs := &fakeBlobs{data: make(map[string][]byte)}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	service := Service{Repository: repo, Blobs: blobs, Tx: passthroughTx{}, Clock: fixedClock{now}, Random: bytes.NewReader(bytes.Repeat([]byte{7}, 32))}
	in := validInput("one")
	record, err := service.Prepare(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != Prepared || record.PostID.Valid() {
		t.Fatalf("unexpected prepared record: %+v", record)
	}
	if _, err := service.Fetch(context.Background(), record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan became public: %v", err)
	}
	record, err = service.Attach(context.Background(), record.ID, "post-1")
	if err != nil {
		t.Fatal(err)
	}
	download, err := service.Fetch(context.Background(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(download.Body)
	_ = download.Body.Close()
	if string(got) != string(in.Data) {
		t.Fatal("download changed")
	}
	if err := service.Delete(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Fetch(context.Background(), record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted attachment public: %v", err)
	}
}

func TestAttachEnforcesOnePerPostAtomically(t *testing.T) {
	repo := newFakeRepository()
	blobs := &fakeBlobs{data: make(map[string][]byte)}
	random := make([]byte, 32)
	for i := range random {
		random[i] = byte(i)
	}
	service := Service{Repository: repo, Blobs: blobs, Tx: passthroughTx{}, Clock: fixedClock{time.Now()}, Random: bytes.NewReader(random)}
	first, err := service.Prepare(context.Background(), validInput("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Prepare(context.Background(), validInput("two"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Attach(context.Background(), first.ID, "post"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Attach(context.Background(), second.ID, "post"); !errors.Is(err, ErrAlreadyAttached) {
		t.Fatalf("got %v", err)
	}
	if got, _ := repo.ByID(context.Background(), second.ID); got.State != Prepared {
		t.Fatalf("failed transition was not atomic: %+v", got)
	}
}

func TestPrepareRepositoryFailureLeavesNonPublicOrphan(t *testing.T) {
	repo := newFakeRepository()
	repo.createErr = errors.New("injected repository failure")
	blobs := &fakeBlobs{data: make(map[string][]byte)}
	service := Service{Repository: repo, Blobs: blobs, Tx: passthroughTx{}, Clock: fixedClock{time.Now()}, Random: bytes.NewReader(make([]byte, 16))}
	if _, err := service.Prepare(context.Background(), validInput("orphan")); err == nil {
		t.Fatal("expected failure")
	}
	if len(blobs.data) != 1 {
		t.Fatal("expected reconciliable content-addressed orphan")
	}
	if len(repo.records) != 0 {
		t.Fatal("failed row became visible")
	}
}

func TestReconcilerPassesOnlyLiveReferencesAndDryRun(t *testing.T) {
	repo := newFakeRepository()
	repo.records["live"] = Record{ID: "live", BlobKey: "keep", State: Attached}
	repo.records["gone"] = Record{ID: "gone", BlobKey: "remove", State: Deleted}
	cleaner := &fakeCleaner{}
	now := time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)
	report, err := (Reconciler{Repository: repo, Cleaner: cleaner, Clock: fixedClock{now}, Grace: time.Hour}).Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cleaner.request.Referenced["keep"]; !ok {
		t.Fatal("live blob omitted")
	}
	if _, ok := cleaner.request.Referenced["remove"]; ok {
		t.Fatal("deleted blob retained")
	}
	if !cleaner.request.DryRun || !cleaner.request.Now.Equal(now) || report.OrphanCandidates != 1 {
		t.Fatalf("bad request/report: %+v %+v", cleaner.request, report)
	}
}

func validInput(body string) Input {
	data := []byte(body)
	return Input{Data: data, MediaType: "image/png", Filename: "x.png", Width: 1, Height: 1, SHA256: sha256.Sum256(data)}
}

type passthroughTx struct{}

func (passthroughTx) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeCleaner struct{ request CleanupRequest }

func (c *fakeCleaner) Cleanup(_ context.Context, r CleanupRequest) (CleanupReport, error) {
	c.request = r
	return CleanupReport{OrphanCandidates: 1}, nil
}

type fakeBlobs struct{ data map[string][]byte }

func (b *fakeBlobs) Put(_ context.Context, in Input) (string, error) {
	key := string(in.SHA256[:])
	b.data[key] = append([]byte{}, in.Data...)
	return key, nil
}
func (b *fakeBlobs) Open(_ context.Context, key, _ string) (io.ReadCloser, int64, error) {
	data, ok := b.data[key]
	if !ok {
		return nil, 0, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}
func (b *fakeBlobs) Delete(_ context.Context, key, _ string) error { delete(b.data, key); return nil }

type fakeRepository struct {
	records   map[domain.AttachmentID]Record
	posts     map[domain.PostID]domain.AttachmentID
	createErr error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{records: make(map[domain.AttachmentID]Record), posts: make(map[domain.PostID]domain.AttachmentID)}
}
func (r *fakeRepository) CreatePrepared(_ context.Context, v Record) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.records[v.ID] = v
	return nil
}
func (r *fakeRepository) ByID(_ context.Context, id domain.AttachmentID) (Record, error) {
	v, ok := r.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return v, nil
}
func (r *fakeRepository) Attach(_ context.Context, id domain.AttachmentID, post domain.PostID) (Record, error) {
	v, ok := r.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	if v.State != Prepared {
		return Record{}, ErrInvalidState
	}
	if _, exists := r.posts[post]; exists {
		return Record{}, ErrAlreadyAttached
	}
	v.State = Attached
	v.PostID = post
	r.posts[post] = id
	r.records[id] = v
	return v, nil
}
func (r *fakeRepository) PublicByID(ctx context.Context, id domain.AttachmentID) (Record, error) {
	v, err := r.ByID(ctx, id)
	if err != nil || v.State != Attached {
		return Record{}, ErrNotFound
	}
	return v, nil
}
func (r *fakeRepository) MarkDeleted(_ context.Context, id domain.AttachmentID, at time.Time) (Record, error) {
	v, ok := r.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	v.State = Deleted
	v.DeletedAt = at
	r.records[id] = v
	delete(r.posts, v.PostID)
	return v, nil
}
func (r *fakeRepository) ReferencedBlobKeys(context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for _, v := range r.records {
		if v.State != Deleted {
			out[v.BlobKey] = struct{}{}
		}
	}
	return out, nil
}

var _ Repository = (*fakeRepository)(nil)
