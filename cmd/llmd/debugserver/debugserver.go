// Package debugserver exposes an opt-in, loopback-only HTTP diagnostics
// surface for llmd: net/http/pprof profiles plus a self-contained
// /debug/statusz page (HTML, or ?format=json). It is mounted on its own
// listener (LLMD_DEBUG_HTTP_ADDR) and deliberately not on the MCP port so
// pprof never shares that surface. All data is computed per-request; there are
// no background collectors.
//
// This package lives at the composition root (llmd/cmd/llmd), not under
// llmd/internal/.
package debugserver

import (
	"database/sql"
	"embed"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed assets/*
var assets embed.FS

// RuntimeCounter reports the number of loaded in-memory runtimes.
type RuntimeCounter interface {
	RuntimeCount() int
}

// DBEntry is one SQLite database to report on.
type DBEntry struct {
	// Label is a human-readable name shown on the page (e.g. "agent").
	Label string
	// Path is the on-disk file path used for file/-wal size reporting. Empty
	// for non-file DSNs (sizes fall back to PRAGMA page_count*page_size).
	Path string
	// DB is the open handle used for PRAGMA + row-count queries.
	DB *sql.DB
}

// Config wires the diagnostics server to live llmd components.
type Config struct {
	StartTime         time.Time
	Databases         []DBEntry
	Runtimes          RuntimeCounter
	EmbeddingCacheDir string
	// Metrics, when set, is served at /metrics (Prometheus exposition).
	Metrics http.Handler
}

// Handler builds the diagnostics mux. Callers are responsible for serving it on
// a loopback-only listener.
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()

	// Explicit pprof mounts (not http.DefaultServeMux) so this surface is
	// self-contained and never leaks onto another listener.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	if cfg.Metrics != nil {
		mux.Handle("/metrics", cfg.Metrics)
	}

	mux.HandleFunc("/debug/statusz", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "json" {
			snap := cfg.snapshot()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			_ = enc.Encode(snap)
			return
		}
		serveAsset(w, "assets/statusz.html", "text/html; charset=utf-8")
	})
	mux.HandleFunc("/debug/statusz.css", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, "assets/statusz.css", "text/css; charset=utf-8")
	})
	mux.HandleFunc("/debug/statusz.js", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, "assets/statusz.js", "application/javascript; charset=utf-8")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/debug/statusz", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

func serveAsset(w http.ResponseWriter, name, contentType string) {
	b, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "asset not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(b)
}

// ---- snapshot model ----------------------------------------------------------

// Snapshot is the JSON payload served at /debug/statusz?format=json and rendered
// client-side by statusz.js.
type Snapshot struct {
	Now           time.Time    `json:"now"`
	UptimeSeconds float64      `json:"uptime_seconds"`
	Go            GoStats      `json:"go"`
	Runtime       RuntimeStats `json:"runtime"`
	Databases     []DBStats    `json:"databases"`
	Paths         PathStats    `json:"paths"`
}

// GoStats captures runtime/GC highlights.
type GoStats struct {
	Version      string `json:"version"`
	Goroutines   int    `json:"goroutines"`
	NumCPU       int    `json:"num_cpu"`
	NumGC        uint32 `json:"num_gc"`
	AllocBytes   uint64 `json:"alloc_bytes"`
	SysBytes     uint64 `json:"sys_bytes"`
	HeapAlloc    uint64 `json:"heap_alloc_bytes"`
	HeapInuse    uint64 `json:"heap_inuse_bytes"`
	HeapSys      uint64 `json:"heap_sys_bytes"`
	StackInuse   uint64 `json:"stack_inuse_bytes"`
}

// RuntimeStats captures llmd runtime gauges.
type RuntimeStats struct {
	LoadedRuntimes int `json:"loaded_runtimes"`
}

// DBStats reports file/page sizes and per-table row counts for one SQLite DB.
type DBStats struct {
	Label         string           `json:"label"`
	Path          string           `json:"path"`
	FileBytes     int64            `json:"file_bytes"`
	WALBytes      int64            `json:"wal_bytes"`
	PageCount     int64            `json:"page_count"`
	PageSize      int64            `json:"page_size"`
	PageBytes     int64            `json:"page_bytes"`
	Tables        []TableRowCount  `json:"tables"`
	Error         string           `json:"error,omitempty"`
}

// TableRowCount is a per-table row count.
type TableRowCount struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// PathStats reports data locations and the embedding cache size.
type PathStats struct {
	EmbeddingCacheDir      string `json:"embedding_cache_dir"`
	EmbeddingCacheDirBytes int64  `json:"embedding_cache_dir_bytes"`
}

func (cfg Config) snapshot() Snapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	snap := Snapshot{
		Now:           time.Now(),
		UptimeSeconds: time.Since(cfg.StartTime).Seconds(),
		Go: GoStats{
			Version:    runtime.Version(),
			Goroutines: runtime.NumGoroutine(),
			NumCPU:     runtime.NumCPU(),
			NumGC:      ms.NumGC,
			AllocBytes: ms.Alloc,
			SysBytes:   ms.Sys,
			HeapAlloc:  ms.HeapAlloc,
			HeapInuse:  ms.HeapInuse,
			HeapSys:    ms.HeapSys,
			StackInuse: ms.StackInuse,
		},
		Paths: PathStats{
			EmbeddingCacheDir:      cfg.EmbeddingCacheDir,
			EmbeddingCacheDirBytes: dirSize(cfg.EmbeddingCacheDir),
		},
	}
	if cfg.Runtimes != nil {
		snap.Runtime.LoadedRuntimes = cfg.Runtimes.RuntimeCount()
	}
	for _, e := range cfg.Databases {
		snap.Databases = append(snap.Databases, dbStats(e))
	}
	return snap
}

func dbStats(e DBEntry) DBStats {
	out := DBStats{Label: e.Label, Path: e.Path}
	if e.Path != "" {
		if fi, err := os.Stat(e.Path); err == nil {
			out.FileBytes = fi.Size()
		}
		if wi, err := os.Stat(e.Path + "-wal"); err == nil {
			out.WALBytes = wi.Size()
		}
	}
	if e.DB == nil {
		return out
	}
	_ = e.DB.QueryRow("PRAGMA page_count").Scan(&out.PageCount)
	_ = e.DB.QueryRow("PRAGMA page_size").Scan(&out.PageSize)
	out.PageBytes = out.PageCount * out.PageSize

	rows, err := e.DB.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		out.Error = err.Error()
		return out
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil {
			names = append(names, n)
		}
	}
	rows.Close()
	for _, n := range names {
		var c int64
		// Table name comes from sqlite_master; quote it to be safe.
		if err := e.DB.QueryRow(`SELECT count(*) FROM "` + n + `"`).Scan(&c); err == nil {
			out.Tables = append(out.Tables, TableRowCount{Name: n, Rows: c})
		}
	}
	return out
}

// dirSize sums file sizes under dir; returns 0 for empty/missing dirs.
func dirSize(dir string) int64 {
	if dir == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
