// Package state stores the dsh launch token between runs.
//
// The token is generated fresh by every "dsh web" process and printed exactly
// once, so a gateway that attaches to an already running dsh has no other way
// to learn it. The file lives inside this repository by default (git-ignored),
// never in the user's dsh home.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// State is the persisted subset of gateway state.
type State struct {
	Token     string    `json:"token,omitempty"`
	Upstream  string    `json:"upstream,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// Load reads path. A missing file is not an error: it yields the zero State.
func Load(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// Clear removes the cached state, ignoring a missing file.
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Save writes path atomically with 0600 permissions, creating the parent
// directory with 0700 when needed.
func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
