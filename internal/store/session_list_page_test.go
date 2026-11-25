package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// sqlCapture records every statement a gorm session runs.
type sqlCapture struct {
	mu   sync.Mutex
	sqls []string
}

func (c *sqlCapture) LogMode(gormlogger.LogLevel) gormlogger.Interface { return c }
func (c *sqlCapture) Info(context.Context, string, ...interface{})     {}
func (c *sqlCapture) Warn(context.Context, string, ...interface{})     {}
func (c *sqlCapture) Error(context.Context, string, ...interface{})    {}
func (c *sqlCapture) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	c.mu.Lock()
	c.sqls = append(c.sqls, sql)
	c.mu.Unlock()
}

// TestListSessionsPaging_AllSorts pages through sessions with and without
// messages for every sort and checks order and last_message_at against values
// computed from the seed data.
func TestListSessionsPaging_AllSorts(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	ws := "ws-page-1"

	type fx struct {
		id      string
		title   string
		created time.Duration
		msgs    []time.Duration
	}
	fixtures := []fx{
		{"a", "Alpha", -50 * time.Hour, []time.Duration{-40 * time.Hour, -3 * time.Hour}},
		{"b", "bravo", -40 * time.Hour, nil},
		{"c", "Charlie", -30 * time.Hour, []time.Duration{-20 * time.Hour}},
		{"d", "delta", -20 * time.Hour, []time.Duration{-1 * time.Hour}},
		{"e", "Echo", -10 * time.Hour, nil},
	}
	wantLast := map[string]time.Time{}
	for _, f := range fixtures {
		seedSession(t, db, repo, ws, f.id, f.title, "pair", f.created, f.msgs...)
		if len(f.msgs) > 0 {
			max := f.msgs[0]
			for _, m := range f.msgs {
				if m > max {
					max = m
				}
			}
			wantLast[f.id] = listTestClock.Add(max)
		}
	}

	// Touch sessions in a different order than creation so updated_at order
	// (c, a, e, b, d ascending) differs from created_at order.
	for _, id := range []string{"c", "a", "e", "b", "d"} {
		time.Sleep(10 * time.Millisecond)
		if err := repo.UpdateSession(ctx, serverutil.Scope{TenantID: ws}, id, map[string]any{"chat_surface": "pair"}); err != nil {
			t.Fatalf("UpdateSession %s: %v", id, err)
		}
	}

	cases := []struct {
		name string
		opts ListSessionsOptions
		want []string
	}{
		{"last_message_desc", ListSessionsOptions{Descending: true}, []string{"d", "a", "e", "c", "b"}},
		{"created_asc", ListSessionsOptions{SortBy: SessionSortCreatedAt}, []string{"a", "b", "c", "d", "e"}},
		{"title_asc", ListSessionsOptions{SortBy: SessionSortTitle}, []string{"a", "b", "c", "d", "e"}},
		{"last_message_asc", ListSessionsOptions{}, []string{"b", "c", "e", "a", "d"}},
		{"updated_asc", ListSessionsOptions{SortBy: SessionSortUpdatedAt}, []string{"c", "a", "e", "b", "d"}},
		{"updated_desc", ListSessionsOptions{SortBy: SessionSortUpdatedAt, Descending: true}, []string{"d", "b", "e", "a", "c"}},
		{"title_desc", ListSessionsOptions{SortBy: SessionSortTitle, Descending: true}, []string{"e", "d", "c", "b", "a"}},
		{"created_desc", ListSessionsOptions{SortBy: SessionSortCreatedAt, Descending: true}, []string{"e", "d", "c", "b", "a"}},
	}
	for _, tc := range cases {
		var got []string
		for off := 0; off < len(fixtures); off += 2 {
			o := tc.opts
			o.Limit = 2
			o.Offset = off
			rows, total, err := repo.ListSessions(ctx, serverutil.Scope{TenantID: ws}, o)
			if err != nil {
				t.Fatalf("%s: ListSessions: %v", tc.name, err)
			}
			if total != int64(len(fixtures)) {
				t.Errorf("%s: total = %d", tc.name, total)
			}
			for _, r := range rows {
				want, has := wantLast[r.SessionID]
				if has != r.LastMessageAt.Valid || (has && !r.LastMessageAt.Time.UTC().Equal(want)) {
					t.Errorf("%s: %s last_message_at = %v (valid=%v), want %v (has=%v)", tc.name, r.SessionID, r.LastMessageAt.Time, r.LastMessageAt.Valid, want, has)
				}
			}
			got = append(got, sessionIDs(rows)...)
		}
		if !equalIDs(got, tc.want...) {
			t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestListSessions_MessageWriteUpdatesLastMessageAt writes messages through
// History.Store and checks llmd_sessions.last_message_at follows created_at.
func TestListSessions_MessageWriteUpdatesLastMessageAt(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ws := "ws-page-4"
	sid := "writer"
	seedSession(t, db, repo, ws, sid, "Writer", "pair", -time.Hour)
	h := NewHistory(db, serverutil.Scope{TenantID: ws}, sid)

	read := func() (last, newest NullTimestamp) {
		t.Helper()
		if err := db.Raw("SELECT last_message_at FROM llmd_sessions WHERE session_id = ?", sid).Row().Scan(&last); err != nil {
			t.Fatalf("select last_message_at: %v", err)
		}
		if err := db.Raw("SELECT MAX(created_at) FROM llmd_chat_messages WHERE session_id = ?", sid).Row().Scan(&newest); err != nil {
			t.Fatalf("select message created_at: %v", err)
		}
		return last, newest
	}

	h.Store(sid, &llmv1.Message{Role: "user", Content: "one", Id: "m1"})
	first, newest := read()
	if !first.Valid || !newest.Valid || !first.Time.Equal(newest.Time) {
		t.Fatalf("after first message last_message_at = %v (valid=%v), want %v", first.Time, first.Valid, newest.Time)
	}

	time.Sleep(20 * time.Millisecond)
	h.Store(sid, &llmv1.Message{Role: "user", Content: "two", Id: "m2"})
	second, newest := read()
	if !second.Valid || !second.Time.Equal(newest.Time) {
		t.Fatalf("after second message last_message_at = %v (valid=%v), want %v", second.Time, second.Valid, newest.Time)
	}
	if !second.Time.After(first.Time) {
		t.Errorf("last_message_at did not move forward: %v then %v", first.Time, second.Time)
	}
}

// TestListSessions_NoMessageAggregate asserts the page query neither joins nor
// aggregates llmd_chat_messages.
func TestListSessions_NoMessageAggregate(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	ws := "ws-page-2"
	seedSession(t, db, NewSessionRepo(db), ws, "x", "X", "pair", -time.Hour, -time.Minute)

	capt := &sqlCapture{}
	repo := NewSessionRepo(db.Session(&gorm.Session{Logger: capt}))
	if _, _, err := repo.ListSessions(context.Background(), serverutil.Scope{TenantID: ws}, ListSessionsOptions{Descending: true, Limit: 5}); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(capt.sqls) == 0 {
		t.Fatal("no SQL captured")
	}
	for _, s := range capt.sqls {
		l := strings.ToLower(s)
		if strings.Contains(l, "llmd_chat_messages") || strings.Contains(l, "max(") {
			t.Errorf("list query touches messages or aggregates: %s", s)
		}
	}
}

// TestListSessions_BackfillsLastMessageAtFromOldSchema starts from the schema
// before last_message_at existed and checks AutoMigrate backfills it.
func TestListSessions_BackfillsLastMessageAtFromOldSchema(t *testing.T) {
	t.Parallel()
	db := NewTestSQLite(t)
	repo := NewSessionRepo(db)
	ws := "ws-page-3"
	seedSession(t, db, repo, ws, "chatty", "Chatty", "pair", -10*time.Hour, -9*time.Hour, -2*time.Hour)
	seedSession(t, db, repo, ws, "silent", "Silent", "pair", -8*time.Hour)

	// Rewind to the old schema: no last_message_at column, no migration records.
	if db.Migrator().HasColumn(&SessionRow{}, "last_message_at") {
		if err := db.Migrator().DropColumn(&SessionRow{}, "last_message_at"); err != nil {
			t.Fatalf("drop column: %v", err)
		}
	}
	if err := db.Exec("DELETE FROM llmd_schema_migrations").Error; err != nil {
		t.Fatalf("clear migrations: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	var got []struct {
		SessionID     string
		LastMessageAt NullTimestamp
	}
	if err := db.Raw("SELECT session_id, last_message_at FROM llmd_sessions WHERE tenant_id = ?", ws).Scan(&got).Error; err != nil {
		t.Fatalf("select last_message_at: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	want := listTestClock.Add(-2 * time.Hour)
	for _, r := range got {
		switch r.SessionID {
		case "chatty":
			if !r.LastMessageAt.Valid || !r.LastMessageAt.Time.UTC().Equal(want) {
				t.Errorf("chatty last_message_at = %v (valid=%v), want %v", r.LastMessageAt.Time, r.LastMessageAt.Valid, want)
			}
		case "silent":
			if r.LastMessageAt.Valid {
				t.Errorf("silent last_message_at = %v, want NULL", r.LastMessageAt.Time)
			}
		default:
			t.Errorf("unexpected session %s", r.SessionID)
		}
	}
}
