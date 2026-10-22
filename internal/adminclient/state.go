package adminclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"bord/internal/client"
)

const stateVersion = 1
const maxStateBytes = 64 << 10

type State struct {
	Version    int    `json:"version"`
	Management string `json:"management"`
	Server     string `json:"server,omitempty"`
	Theme      string `json:"theme,omitempty"`
	View       string `json:"view,omitempty"`
	Mode       string `json:"mode,omitempty"`
	InlineMode string `json:"inline_mode,omitempty"`
}

type StateStore struct{ path string }

func DefaultStateStore() (*StateStore, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return nil, errors.New("resolve admin state directory")
	}
	return NewStateStore(filepath.Join(directory, "bord", "admin.json")), nil
}

func NewStateStore(path string) *StateStore { return &StateStore{path: path} }

func (s *StateStore) Load() (State, error) {
	data, err := client.ReadPrivateFile(s.path, maxStateBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, errors.New("read admin state")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil || state.Version != stateVersion {
		return State{}, errors.New("invalid admin state")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return State{}, errors.New("invalid admin state")
	}
	return state, nil
}

func (s *StateStore) Save(state State) error {
	state.Version = stateVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.New("encode admin state")
	}
	return client.WritePrivateFile(s.path, data)
}
