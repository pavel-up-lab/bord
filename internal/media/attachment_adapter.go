package media

import (
	"context"
	"io"

	"bord/internal/app/attachment"
)

// AttachmentStore adapts Store to the attachment use-case port.
type AttachmentStore struct{ Store *Store }

func (m AttachmentStore) Put(ctx context.Context, in attachment.Input) (string, error) {
	blob, err := m.Store.Put(ctx, Image{Data: in.Data, MediaType: in.MediaType, Filename: in.Filename, Width: in.Width, Height: in.Height, SHA256: in.SHA256})
	return blob.Key, err
}

func (m AttachmentStore) Open(ctx context.Context, key, mediaType string) (io.ReadCloser, int64, error) {
	reader, blob, err := m.Store.Open(ctx, key, mediaType)
	return reader, blob.Size, err
}

func (m AttachmentStore) Delete(ctx context.Context, key, mediaType string) error {
	return m.Store.Delete(ctx, key, mediaType)
}

func (m AttachmentStore) Cleanup(ctx context.Context, request attachment.CleanupRequest) (attachment.CleanupReport, error) {
	report, err := m.Store.Cleanup(ctx, CleanupOptions{Now: request.Now, Grace: request.Grace, Referenced: request.Referenced, DryRun: request.DryRun})
	return attachment.CleanupReport{
		TemporaryCandidates: len(report.TemporaryCandidates),
		OrphanCandidates:    len(report.OrphanCandidates),
		Removed:             len(report.Removed),
		Bytes:               report.Bytes,
	}, err
}

var _ attachment.BlobStore = AttachmentStore{}
var _ attachment.Cleaner = AttachmentStore{}
