/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import "testing"

func TestStateUnsetEnabledDefaultsToEnabled(t *testing.T) {
	store := NewManagedStateStore(t.TempDir())
	got := store.Get()
	if !got.Enabled {
		t.Fatal("unset enabled flag must read back as enabled")
	}
	if got.ActiveModelID != "" {
		t.Fatalf("active model = %q, want empty", got.ActiveModelID)
	}
}

func TestStateSetEnabledFalsePersists(t *testing.T) {
	dir := t.TempDir()
	store := NewManagedStateStore(dir)
	if _, err := store.SetEnabled(false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	// Reload through a fresh store: the value must come off disk.
	if NewManagedStateStore(dir).Get().Enabled {
		t.Fatal("expected enabled=false after reload")
	}
	if _, err := store.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if !NewManagedStateStore(dir).Get().Enabled {
		t.Fatal("expected enabled=true after re-enabling")
	}
}

func TestStateSetActiveModelTrimsAndClears(t *testing.T) {
	dir := t.TempDir()
	store := NewManagedStateStore(dir)
	state, err := store.SetActiveModel("  m  ")
	if err != nil {
		t.Fatalf("SetActiveModel: %v", err)
	}
	if state.ActiveModelID != "m" {
		t.Fatalf("active model = %q, want %q", state.ActiveModelID, "m")
	}
	if got := NewManagedStateStore(dir).Get().ActiveModelID; got != "m" {
		t.Fatalf("reloaded active model = %q, want %q", got, "m")
	}

	cleared, err := store.SetActiveModel("")
	if err != nil {
		t.Fatalf("SetActiveModel: %v", err)
	}
	if cleared.ActiveModelID != "" {
		t.Fatalf("empty id should clear the pin, got %q", cleared.ActiveModelID)
	}
	if !cleared.Enabled {
		t.Fatal("clearing the model must not disable the provider")
	}
}

func TestStateManagedPickerLabel(t *testing.T) {
	if got := ManagedPickerLabel("gemma-3-12b-it"); got != "Local LLAMA gemma-3-12b-it" {
		t.Errorf("label = %q", got)
	}
	if got := ManagedPickerLabel("   "); got != "Local LLAMA (no model)" {
		t.Errorf("empty label = %q", got)
	}
}
