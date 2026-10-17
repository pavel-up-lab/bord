package client

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var cacheID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type Cache struct{ dir string }

func NewCache(dir string) *Cache { return &Cache{dir: dir} }

func DefaultCache() (*Cache, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, errors.New("locate user cache directory")
	}
	return NewCache(filepath.Join(dir, "bord", "attachments")), nil
}

func (c *Cache) Put(id, expectedSHA256 string, body []byte) (string, error) {
	if !cacheID.MatchString(id) {
		return "", errors.New("invalid attachment cache ID")
	}
	expected, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(expected) != sha256.Size {
		return "", errors.New("invalid attachment checksum")
	}
	sum := sha256.Sum256(body)
	actual := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return "", errors.New("attachment checksum mismatch")
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return "", errors.New("create attachment cache")
	}
	if err := secureDir(c.dir); err != nil {
		return "", errors.New("secure attachment cache")
	}
	target := filepath.Join(c.dir, fmt.Sprintf("%s-%s", id, actual[:16]))
	tmp, err := os.CreateTemp(c.dir, ".attachment-*")
	if err != nil {
		return "", errors.New("create cached attachment")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", errors.New("secure cached attachment")
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return "", errors.New("write cached attachment")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", errors.New("sync cached attachment")
	}
	if err := tmp.Close(); err != nil {
		return "", errors.New("close cached attachment")
	}
	if err := replaceFile(name, target); err != nil {
		return "", errors.New("publish cached attachment")
	}
	if err := syncParent(target); err != nil {
		return "", errors.New("sync attachment cache")
	}
	return target, nil
}
