package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func normalizedPNG(t *testing.T) Image {
	t.Helper()
	image, err := processor().Normalize(context.Background(), mustOpen(t, "testdata/valid.png"), "valid.png")
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func mustOpen(t *testing.T, path string) io.Reader {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestStoreRoundTripContentAddressed(t *testing.T) {
	store, err := NewStore(t.TempDir(), SyncData)
	if err != nil {
		t.Fatal(err)
	}
	image := normalizedPNG(t)
	blob, err := store.Put(context.Background(), image)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(store.root, "blobs", blob.Key[:2], blob.Key[2:4], blob.Key+".png")
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	r, opened, err := store.Open(context.Background(), blob.Key, PNG)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(image.Data) || opened.SHA256 != image.SHA256 {
		t.Fatal("blob changed")
	}
}

func TestStoreFailuresNeverExposePartialBlob(t *testing.T) {
	image := normalizedPNG(t)
	for _, test := range []struct {
		name   string
		inject func(*Store)
	}{
		{"write", func(s *Store) {
			s.write = func(_ context.Context, w io.Writer, p []byte) (int, error) {
				n, _ := w.Write(p[:len(p)/2])
				return n, errors.New("injected write")
			}
		}},
		{"rename", func(s *Store) { s.rename = func(string, string) error { return errors.New("injected rename") } }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(t.TempDir(), SyncNone)
			if err != nil {
				t.Fatal(err)
			}
			test.inject(store)
			if _, err := store.Put(context.Background(), image); err == nil {
				t.Fatal("expected failure")
			}
			key := fmtHash(image.SHA256)
			if _, err := os.Stat(filepath.Join(store.root, "blobs", key[:2], key[2:4], key+".png")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial blob visible: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(store.root, "tmp"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("temporary files leaked: %v", entries)
			}
		})
	}
}

func TestCleanupDryRunGraceAndReferences(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root, SyncNone)
	if err != nil {
		t.Fatal(err)
	}
	image := normalizedPNG(t)
	blob, err := store.Put(context.Background(), image)
	if err != nil {
		t.Fatal(err)
	}
	orphan := image
	orphan.Data = append([]byte{}, image.Data...)
	orphan.Data = append(orphan.Data, 0)
	orphan.SHA256 = sha256Bytes(orphan.Data)
	// Store rejects malformed normalized content only by digest/media contract, which is sufficient for this fixture.
	orphanBlob, err := store.Put(context.Background(), orphan)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(root, "tmp", ".incoming-old")
	if err := os.WriteFile(tmp, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, path := range []string{tmp, filepath.Join(root, "blobs", orphanBlob.Key[:2], orphanBlob.Key[2:4], orphanBlob.Key+".png"), filepath.Join(root, "blobs", blob.Key[:2], blob.Key[2:4], blob.Key+".png")} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	opts := CleanupOptions{Now: time.Now(), Grace: time.Hour, Referenced: map[string]struct{}{blob.Key: {}}, DryRun: true}
	report, err := store.Cleanup(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.TemporaryCandidates) != 1 || len(report.OrphanCandidates) != 1 || len(report.Removed) != 0 {
		t.Fatalf("unexpected dry-run: %+v", report)
	}
	opts.DryRun = false
	report, err = store.Cleanup(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Removed) != 2 {
		t.Fatalf("unexpected cleanup: %+v", report)
	}
	reader, _, err := store.Open(context.Background(), blob.Key, PNG)
	if err != nil {
		t.Fatal("referenced blob removed")
	}
	_ = reader.Close()
}

func fmtHash(sum [32]byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range sum {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&15]
	}
	return string(out)
}
