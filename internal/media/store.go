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
	"sync"
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
	fs       *os.Root
	sync     SyncPolicy
	mu       sync.Mutex
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
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o700); err != nil {
		return nil, err
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	s := &Store{root: root, fs: rootFS, sync: policy, write: writeContext}
	s.rename = rootFS.Rename
	s.openFile = rootFS.OpenFile
	if policy == SyncDataAndDirectory {
		if err := s.syncDirectory("."); err != nil {
			_ = rootFS.Close()
			return nil, fmt.Errorf("sync store directory: %w", err)
		}
	}
	return s, nil
}

// Close releases the directory handle used to confine all store operations.
func (s *Store) Close() error { return s.fs.Close() }

// Put writes a private temporary file and only exposes the final path after a
// complete write (and configured fsync) succeeds.
func (s *Store) Put(ctx context.Context, image Image) (Blob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	dir := filepath.Join("blobs", key[:2], key[2:4])
	if err := s.fs.MkdirAll(dir, 0o700); err != nil {
		return Blob{}, err
	}
	dest := filepath.Join(dir, key+ext)
	if info, err := s.fs.Lstat(dest); err == nil {
		if !info.Mode().IsRegular() {
			return Blob{}, errors.New("media: existing content-addressed path is not a regular file")
		}
		if info.Size() != int64(len(image.Data)) {
			return Blob{}, errors.New("media: existing content-addressed blob has wrong size")
		}
		existing, err := s.fs.Open(dest)
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
		// A prepared row is created after Put returns. Refreshing a deduplicated
		// blob keeps an old orphan from being collected during that window.
		now := time.Now()
		if err := s.fs.Chtimes(dest, now, now); err != nil {
			return Blob{}, fmt.Errorf("refresh existing blob: %w", err)
		}
		return Blob{Key: key, MediaType: image.MediaType, Size: int64(len(image.Data)), SHA256: image.SHA256}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Blob{}, err
	}
	tmp := filepath.Join("tmp", ".incoming-"+key+fmt.Sprintf("-%d", time.Now().UnixNano()))
	f, err := s.openFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Blob{}, fmt.Errorf("create temporary blob: %w", err)
	}
	remove := true
	defer func() {
		if remove {
			_ = s.fs.Remove(tmp)
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
		for _, path := range []string{"blobs", filepath.Join("blobs", key[:2]), dir} {
			if err := s.syncDirectory(path); err != nil {
				return Blob{}, fmt.Errorf("sync blob directory: %w", err)
			}
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
	info, err := s.fs.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return nil, Blob{}, ErrBlobNotFound
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, Blob{}, ErrBlobNotFound
	}
	if err != nil {
		return nil, Blob{}, err
	}
	f, err := s.fs.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Blob{}, ErrBlobNotFound
	}
	if err != nil {
		return nil, Blob{}, err
	}
	info, err = f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Blob{}, err
	}
	return f, Blob{Key: key, MediaType: mediaType, Size: info.Size(), SHA256: sum}, nil
}

func (s *Store) Delete(ctx context.Context, key, mediaType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	path, _, err := s.path(key, mediaType)
	if err != nil {
		return err
	}
	err = s.fs.Remove(path)
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
	return filepath.Join("blobs", key[:2], key[2:4], key+ext), sum, nil
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

func (s *Store) syncDirectory(path string) error {
	f, err := s.fs.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
