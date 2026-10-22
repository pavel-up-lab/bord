package adminclient

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxCredentialBytes = 4096

func DefaultCredentialPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("resolve admin credential directory")
	}
	return filepath.Join(directory, "bord", "admin.credential"), nil
}

// LoadCredential applies the explicit security order: secret environment,
// explicit/file environment path, then the private per-user default file.
func LoadCredential(explicitPath string) (string, error) {
	if value, present := os.LookupEnv("BORD_ADMIN_CREDENTIAL"); present {
		return normalizeCredential(value)
	}
	path := explicitPath
	if path == "" {
		path = os.Getenv("BORD_ADMIN_CREDENTIAL_FILE")
	}
	if path == "" {
		var err error
		path, err = DefaultCredentialPath()
		if err != nil {
			return "", err
		}
	}
	return readCredentialFile(path)
}

func readCredentialFile(path string) (string, error) {
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil {
		return "", errors.New("read admin credential file")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("admin credential must be a regular non-symlink file")
	}
	if err := validateCredentialFileOwner(info); err != nil {
		return "", err
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", errors.New("read admin credential file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil || len(data) > maxCredentialBytes {
		return "", errors.New("invalid admin credential file")
	}
	return normalizeCredential(string(data))
}

func normalizeCredential(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", errors.New("invalid admin credential")
	}
	return value, nil
}
