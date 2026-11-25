package server

import (
	"context"
	"testing"

	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/grpcauth"
	"github.com/johnewart/pyxis/internal/store"
)

func TestUsage_RecordsClientID(t *testing.T) {
	_, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
	s := &LlmServer{Usage: store.NewUsageRepo(db)}

	s.recordUsage(grpcauth.WithClientID(context.Background(), "client-a"), testScope, usageCall{rpc: "ChatStream", model: "m"})
	s.recordUsage(context.Background(), testScope, usageCall{rpc: "ChatStream", model: "m"})

	var rows []store.UsageRecordRow
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d want 2", len(rows))
	}
	if rows[0].ClientID != "client-a" {
		t.Fatalf("authenticated row client_id=%q want client-a", rows[0].ClientID)
	}
	if rows[1].ClientID != "" {
		t.Fatalf("unauthenticated row client_id=%q want empty", rows[1].ClientID)
	}
}
