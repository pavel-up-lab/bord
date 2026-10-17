package client

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const stateVersion = 1

// State contains local client settings and credentials. It must never be logged as a value.
type State struct {
	Version       int               `json:"version"`
	Server        string            `json:"server"`
	LastBoard     string            `json:"last_board,omitempty"`
	InlineMode    string            `json:"inline_mode,omitempty"`
	SessionSecret string            `json:"session_secret"`
	DeleteSecrets map[string]string `json:"delete_secrets,omitempty"`
}

func (State) String() string { return "<redacted client state>" }

type Store struct{ path string }

func DefaultStore() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, errors.New("locate user configuration directory")
	}
	return NewStore(filepath.Join(dir, "bord", "state.json")), nil
}

func NewStore(path string) *Store { return &Store{path: path} }

func NewState(server string) (State, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return State{}, errors.New("generate session secret")
	}
	return State{Version: stateVersion, Server: server, InlineMode: "auto", SessionSecret: base64.RawURLEncoding.EncodeToString(secret), DeleteSecrets: map[string]string{}}, nil
}

func NewDeleteSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", errors.New("generate delete secret")
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}

// Load returns a zero State when the file does not exist. Corrupt files are quarantined.
func (s *Store) Load() (State, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, errors.New("read local client state")
	}
	var state State
	if err := json.Unmarshal(b, &state); err != nil || state.Version != stateVersion {
		backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
		if renameErr := os.Rename(s.path, backup); renameErr != nil {
			return State{}, errors.New("recover corrupt local client state")
		}
		return State{}, nil
	}
	if state.DeleteSecrets == nil {
		state.DeleteSecrets = map[string]string{}
	}
	return state, nil
}

func (s *Store) Save(state State) error {
	state.Version = stateVersion
	if state.DeleteSecrets == nil {
		state.DeleteSecrets = map[string]string{}
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.New("encode local client state")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("create local client state directory")
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return errors.New("create temporary local client state")
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return errors.New("secure temporary local client state")
	}
	if _, err := tmp.Write(b); err != nil {
		return errors.New("write temporary local client state")
	}
	if err := tmp.Sync(); err != nil {
		return errors.New("sync temporary local client state")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("close temporary local client state")
	}
	if err := replaceFile(tmpName, s.path); err != nil {
		return errors.New("replace local client state")
	}
	if err := os.Chmod(s.path, 0o600); err != nil && os.PathSeparator == '/' {
		return errors.New("secure local client state")
	}
	ok = true
	return nil
}
