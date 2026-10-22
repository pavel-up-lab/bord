package client

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// WritePrivateFile atomically replaces one per-user configuration file using
// the same cross-platform durability and permission rules as the Bord state.
func WritePrivateFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return errors.New("create private configuration directory")
	}
	temporary, err := os.CreateTemp(directory, ".private-*")
	if err != nil {
		return errors.New("create private temporary file")
	}
	temporaryPath := temporary.Name()
	complete := false
	defer func() {
		_ = temporary.Close()
		if !complete {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.New("secure private temporary file")
	}
	if _, err := temporary.Write(data); err != nil {
		return errors.New("write private temporary file")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync private temporary file")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close private temporary file")
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return errors.New("replace private configuration file")
	}
	if err := secureFile(path); err != nil {
		return errors.New("secure private configuration file")
	}
	if err := syncParent(path); err != nil {
		return errors.New("sync private configuration directory")
	}
	complete = true
	return nil
}

func ReadPrivateFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("private configuration must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := secureFile(path); err != nil {
		return nil, errors.New("secure private configuration file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("invalid private configuration file")
	}
	return data, nil
}
