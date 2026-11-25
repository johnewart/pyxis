package sessionmanager

import (
	"testing"

	"github.com/johnewart/pyxis/internal/assistant/history"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

func TestNew_requiresSessionPersistAndNewSessionHistory(t *testing.T) {
	hub := session.HubOptions{}
	if _, err := New(hub, Options{}); err != ErrMissingStore {
		t.Fatalf("empty options: %v", err)
	}
	var sessionRepo store.SessionRepo
	if _, err := New(hub, Options{SessionPersist: &sessionRepo}); err != ErrMissingStore {
		t.Fatalf("missing NewSessionHistory: %v", err)
	}
	if _, err := New(hub, Options{
		NewSessionHistory: func(serverutil.Scope, string) history.Store { return nil },
	}); err != ErrMissingStore {
		t.Fatalf("missing SessionPersist: %v", err)
	}
}
