/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// App-global managed-llama state.
//
// Both the on/off flag and the active model are deliberately app-wide rather
// than per tenant or scope: there is a single managed llama-server process, so a
// per-project model choice would make projects evict each other's model on
// every switch — a multi-gigabyte reload each time.
//
// llmd owns this state (its own JSON file under the state dir); a client asks
// for it over RPC and never keeps a parallel authority.

package llamacpp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ManagedStateRelPath is the state file location under the state dir.
const ManagedStateRelPath = "llama-server/managed-state.json"

// ManagedState is the app-global managed llama.cpp state.
type ManagedState struct {
	Enabled bool `json:"enabled"`
	// ActiveModelID is the model the single managed server should load; empty
	// when none is chosen.
	ActiveModelID string `json:"activeModelId"`
}

// persisted mirrors ManagedState but keeps Enabled as a pointer so that
// "never explicitly set" stays distinguishable from "set to false". Unset means
// enabled — the `managed_llama_enabled !== false` default from the TS original,
// which keeps installs predating the toggle working exactly as they did.
type persisted struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	ActiveModelID string `json:"activeModelId,omitempty"`
}

// ManagedStateStore reads and writes the app-global managed state.
type ManagedStateStore struct {
	path string
	mu   sync.Mutex
}

// NewManagedStateStore builds a store over <stateDir>/llama-server/managed-state.json.
func NewManagedStateStore(stateDir string) *ManagedStateStore {
	return &ManagedStateStore{path: filepath.Join(stateDir, filepath.FromSlash(ManagedStateRelPath))}
}

// Path is the file this store persists to.
func (s *ManagedStateStore) Path() string { return s.path }

func (s *ManagedStateStore) readLocked() persisted {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return persisted{}
	}
	var out persisted
	if err := json.Unmarshal(raw, &out); err != nil {
		return persisted{}
	}
	return out
}

func (s *ManagedStateStore) writeLocked(state persisted) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create managed state dir: %w", err)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed state: %w", err)
	}
	if err := os.WriteFile(s.path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write managed state: %w", err)
	}
	return nil
}

func resolve(state persisted) ManagedState {
	return ManagedState{
		// Unset reads as enabled, never as disabled.
		Enabled:       state.Enabled == nil || *state.Enabled,
		ActiveModelID: strings.TrimSpace(state.ActiveModelID),
	}
}

// Get returns the current app-global state. A missing or corrupt file yields
// the defaults (enabled, no model) rather than an error.
func (s *ManagedStateStore) Get() ManagedState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return resolve(s.readLocked())
}

// SetEnabled flips the app-wide managed provider on or off.
func (s *ManagedStateStore) SetEnabled(enabled bool) (ManagedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.readLocked()
	state.Enabled = &enabled
	if err := s.writeLocked(state); err != nil {
		return resolve(state), err
	}
	return resolve(state), nil
}

// SetActiveModel pins the single model the managed server should serve. An
// empty id clears the pin and leaves the provider enabled with no model, which
// pickers render as "Local LLAMA (no model)".
func (s *ManagedStateStore) SetActiveModel(modelID string) (ManagedState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.readLocked()
	state.ActiveModelID = strings.TrimSpace(modelID)
	if err := s.writeLocked(state); err != nil {
		return resolve(state), err
	}
	return resolve(state), nil
}

// ManagedPickerLabel is the label shown in model pickers, e.g.
// "Local LLAMA gemma-3-12b-it". Enabled-with-nothing-chosen is a real,
// reachable state (fresh install, or the user cleared the model), so it is
// surfaced rather than hidden — hiding it makes the enable toggle look broken.
func ManagedPickerLabel(activeModelID string) string {
	model := strings.TrimSpace(activeModelID)
	if model == "" {
		return "Local LLAMA (no model)"
	}
	return "Local LLAMA " + model
}
