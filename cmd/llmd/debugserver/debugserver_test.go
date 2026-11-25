package debugserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type fakeRuntimes int

func (f fakeRuntimes) RuntimeCount() int { return int(f) }

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY);
		INSERT INTO widgets DEFAULT VALUES;
		INSERT INTO widgets DEFAULT VALUES;`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return Handler(Config{
		StartTime: time.Now().Add(-90 * time.Second),
		Databases: []DBEntry{{Label: "agent", DB: db}},
		Runtimes:  fakeRuntimes(3),
	})
}

func TestStatuszJSON(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/debug/statusz?format=json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	var snap Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.UptimeSeconds < 60 {
		t.Errorf("uptime = %v, want >= 60", snap.UptimeSeconds)
	}
	if snap.Runtime.LoadedRuntimes != 3 {
		t.Errorf("loaded runtimes = %d, want 3", snap.Runtime.LoadedRuntimes)
	}
	if snap.Go.Goroutines < 1 {
		t.Errorf("goroutines = %d, want >= 1", snap.Go.Goroutines)
	}
	if len(snap.Databases) != 1 {
		t.Fatalf("databases = %d, want 1", len(snap.Databases))
	}
	db := snap.Databases[0]
	var widgets *TableRowCount
	for i := range db.Tables {
		if db.Tables[i].Name == "widgets" {
			widgets = &db.Tables[i]
		}
	}
	if widgets == nil || widgets.Rows != 2 {
		t.Errorf("widgets row count = %+v, want 2", widgets)
	}
}

func TestStatuszHTMLAndAssets(t *testing.T) {
	h := newTestHandler(t)
	for _, tc := range []struct {
		path, wantCT string
	}{
		{"/debug/statusz", "text/html; charset=utf-8"},
		{"/debug/statusz.css", "text/css; charset=utf-8"},
		{"/debug/statusz.js", "application/javascript; charset=utf-8"},
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", tc.path, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != tc.wantCT {
			t.Errorf("%s content-type = %q, want %q", tc.path, ct, tc.wantCT)
		}
		if rr.Body.Len() == 0 {
			t.Errorf("%s empty body", tc.path)
		}
	}
}

func TestPprofMounted(t *testing.T) {
	h := newTestHandler(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("pprof goroutine status = %d, want 200", rr.Code)
	}
}

func TestRootRedirect(t *testing.T) {
	h := newTestHandler(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("root status = %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/debug/statusz" {
		t.Errorf("redirect location = %q", loc)
	}
}
