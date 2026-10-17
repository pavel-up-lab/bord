package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrBlobNotFound = errors.New("media: blob not found")

type Blob struct {
	Key       string
	MediaType string
	Size      int64
	SHA256    [32]byte
}

type SyncPolicy int

const (
	SyncNone SyncPolicy = iota
	SyncData
	SyncDataAndDirectory
)

type Store struct {
	root     string
	sync     SyncPolicy
	rename   func(string, string) error
	openFile func(string, int, os.FileMode) (*os.File, error)
	write    func(context.Context, io.Writer, []byte) (int, error)
}

func NewStore(root string, policy SyncPolicy) (*Store, error) {
	if root == "" {
		return nil, errors.New("media: empty store root")
	}
	if policy < SyncNone || policy > SyncDataAndDirectory {
		return nil, errors.New("media: invalid sync policy")
	}
	s := &Store{root: root, sync: policy, rename: os.Rename, openFile: os.OpenFile, write: writeContext}
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o700); err != nil {
		return nil, err
	}
	return s, nil
}

// Put writes a private temporary file and only exposes the final path after a
// complete write (and configured fsync) succeeds.
func (s *Store) Put(ctx context.Context, image Image) (Blob, error) {
	if err := ctx.Err(); err != nil {
		return Blob{}, err
	}
	if len(image.Data) == 0 || image.SHA256 != sha256Bytes(image.Data) {
		return Blob{}, ErrInvalid
	}
	ext, err := Extension(image.MediaType)
	if err != nil {
		return Blob{}, err
	}
	key := hex.EncodeToString(image.SHA256[:])
	dir := filepath.Join(s.root, "blobs", key[:2], key[2:4])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Blob{}, err
	}
	dest := filepath.Join(dir, key+ext)
	if info, err := os.Stat(dest); err == nil && info.Mode().IsRegular() {
		if info.Size() != int64(len(image.Data)) {
			return Blob{}, errors.New("media: existing content-addressed blob has wrong size")
		}
		existing, err := os.Open(dest)
		if err != nil {
			return Blob{}, err
		}
		hash := sha256.New()
		_, hashErr := io.Copy(hash, existing)
		closeErr := existing.Close()
		if hashErr != nil {
			return Blob{}, hashErr
		}
		if closeErr != nil {
			return Blob{}, closeErr
		}
		// Size plus the content-addressed name are not enough if an operator or
		// disk fault corrupted the file. Verify bytes before reporting success.
		if !bytes.Equal(hash.Sum(nil), image.SHA256[:]) {
			return Blob{}, errors.New("media: existing content-addressed blob is corrupt")
		}
		return Blob{Key: key, MediaType: image.MediaType, Size: int64(len(image.Data)), SHA256: image.SHA256}, nil
	}
	tmp := filepath.Join(s.root, "tmp", ".incoming-"+key+fmt.Sprintf("-%d", time.Now().UnixNano()))
	f, err := s.openFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Blob{}, fmt.Errorf("create temporary blob: %w", err)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tmp)
		}
	}()
	written, writeErr := s.write(ctx, f, image.Data)
	if writeErr == nil && written != len(image.Data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil && s.sync >= SyncData {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return Blob{}, fmt.Errorf("write temporary blob: %w", writeErr)
	}
	if closeErr != nil {
		return Blob{}, fmt.Errorf("close temporary blob: %w", closeErr)
	}
	if err := s.rename(tmp, dest); err != nil {
		return Blob{}, fmt.Errorf("publish blob: %w", err)
	}
	remove = false
	if s.sync == SyncDataAndDirectory {
		if err := syncDir(dir); err != nil {
			return Blob{}, fmt.Errorf("sync blob directory: %w", err)
		}
	}
	return Blob{Key: key, MediaType: image.MediaType, Size: int64(len(image.Data)), SHA256: image.SHA256}, nil
}

func (s *Store) Open(ctx context.Context, key, mediaType string) (io.ReadCloser, Blob, error) {
	if err := ctx.Err(); err != nil {
		return nil, Blob{}, err
	}
	path, sum, err := s.path(key, mediaType)
	if err != nil {
		return nil, Blob{}, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Blob{}, ErrBlobNotFound
	}
	if err != nil {
		return nil, Blob{}, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Blob{}, err
	}
	return f, Blob{Key: key, MediaType: mediaType, Size: info.Size(), SHA256: sum}, nil
}

func (s *Store) Delete(ctx context.Context, key, mediaType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, _, err := s.path(key, mediaType)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) path(key, mediaType string) (string, [32]byte, error) {
	var sum [32]byte
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != len(sum) || strings.ToLower(key) != key {
		return "", sum, ErrInvalid
	}
	copy(sum[:], decoded)
	ext, err := Extension(mediaType)
	if err != nil {
		return "", sum, err
	}
	return filepath.Join(s.root, "blobs", key[:2], key[2:4], key+ext), sum, nil
}

func writeContext(ctx context.Context, w io.Writer, data []byte) (int, error) {
	total := 0
	for total < len(data) {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := w.Write(data[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
