package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

func openFileStore(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "llmd.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}
	})
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// The incident: while a turn streamed, other sessions' AppendEvent calls failed
// in well under a millisecond with "database is locked" (append failed after 3
// attempts) behind a raw-chunk insert that held the write lock. AppendEvent
// reads MAX(seq) and then inserts; as a deferred transaction that upgrade fails
// at once, ignoring busy_timeout. Appends must wait for the writer instead.
func TestAppendEventWaitsForAnotherWriter(t *testing.T) {
	db := openFileStore(t)
	if err := db.Exec("CREATE TABLE probe (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	repo := NewSessionEventRepo(db)
	sc := serverutil.Scope{TenantID: "tenant", ScopeID: "scope"}

	holding := make(chan struct{})
	release := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("INSERT INTO probe (id) VALUES (1)").Error; err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	const sessions, each = 6, 5
	var wg sync.WaitGroup
	errs := make(chan error, sessions*each)
	for s := 0; s < sessions; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, err := repo.AppendEvent(context.Background(), sc, fmt.Sprintf("session-%d", s), `{"n":1}`); err != nil {
					errs <- err
				}
			}
		}(s)
	}
	// Keep the lock long enough that a deferred transaction would certainly have
	// tried, and failed, to upgrade.
	time.Sleep(300 * time.Millisecond)
	close(release)
	if err := <-writerDone; err != nil {
		t.Fatalf("writer: %v", err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("append failed while another connection was writing: %v", err)
	}
	for s := 0; s < sessions; s++ {
		last, err := repo.LastSeq(context.Background(), sc, fmt.Sprintf("session-%d", s))
		if err != nil {
			t.Fatal(err)
		}
		if last != each {
			t.Fatalf("session-%d last seq = %d, want %d", s, last, each)
		}
	}
}

func TestWithTxLock(t *testing.T) {
	cases := map[string]string{
		"llmd.sqlite":                          "llmd.sqlite?_txlock=immediate",
		"file:llmd.sqlite?_busy_timeout=5000":  "file:llmd.sqlite?_busy_timeout=5000&_txlock=immediate",
		"file:x.sqlite?_txlock=exclusive":      "file:x.sqlite?_txlock=exclusive",
		"file:t?mode=memory&cache=shared":      "file:t?mode=memory&cache=shared",
		":memory:":                             ":memory:",
	}
	for in, want := range cases {
		if got := withTxLock(in); got != want {
			t.Errorf("withTxLock(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Count(withTxLock("a.sqlite?_txlock=immediate"), "_txlock") != 1 {
		t.Error("an existing _txlock must not be repeated")
	}
}
