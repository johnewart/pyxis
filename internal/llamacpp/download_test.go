/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// testEntry mirrors the catalog preset used by llamacpp.download.test.ts.
func testEntry() CatalogEntry {
	return CatalogEntry{
		ID:           "gemma-4-26b-a4b-it",
		HFRepo:       "example-org/gemma-4-26b-a4b-it-GGUF",
		HFFile:       "gemma-4-26b-a4b-it-Q4_K_M.gguf",
		BaseWeightGb: 17.0,
	}
}

func newDownloadManager(t *testing.T, base string) *Manager {
	t.Helper()
	return New(Options{
		CacheDir:  filepath.Join(t.TempDir(), "llama-server"),
		ModelsDir: t.TempDir(),
		HFBase:    base,
		SizeStore: NewMemoryDownloadSizeStore(),
	})
}

func TestDownloadSizeClassification(t *testing.T) {
	if !IsDownloadFileSizeValid(4096, 4096) {
		t.Error("exact size match should be valid")
	}
	if IsDownloadFileSizeValid(1024, 4096) {
		t.Error("size mismatch should be invalid")
	}
	if IsDownloadFileSizeValid(0, 4096) || IsDownloadFileSizeValid(4096, 0) {
		t.Error("non-positive sizes should be invalid")
	}

	if got := CatalogBytesFromWeightGb(17); got != 17_000_000_000 {
		t.Errorf("CatalogBytesFromWeightGb(17) = %d", got)
	}
	if got := CatalogBytesFromWeightGb(0); got != 0 {
		t.Errorf("CatalogBytesFromWeightGb(0) = %d, want 0", got)
	}
	if got := CatalogMinCompleteBytes(17); got != 8_500_000_000 {
		t.Errorf("CatalogMinCompleteBytes(17) = %d", got)
	}
	// 50% of a tiny weight still floors at 1 MB.
	if got := CatalogMinCompleteBytes(0.001); got != 1_000_000 {
		t.Errorf("CatalogMinCompleteBytes(0.001) = %d, want 1000000", got)
	}

	cases := []struct {
		name                           string
		downloaded, expected, minBytes int64
		want                           DownloadFileStatus
	}{
		{"absent", 0, 0, 0, StatusMissing},
		{"exact match", 4096, 4096, 0, StatusComplete},
		{"below expected", 1024, 17_010_978_592, 8_500_000_000, StatusPartial},
		{"below floor without expected", 1024, 0, 8_500_000_000, StatusPartial},
		{"legacy file, no expectations", 18, 0, 0, StatusComplete},
		{"mismatch above expected", 5000, 4096, 0, StatusComplete},
	}
	for _, c := range cases {
		if got := ClassifyCatalogDownloadFile(c.downloaded, c.expected, c.minBytes); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	if _, err := CatalogDownloadSizeKey("  ", FileKindMain); err == nil {
		t.Error("blank catalog id should be rejected")
	}
	key, err := CatalogDownloadSizeKey("abc", FileKindMmproj)
	if err != nil || key != "abc::mmproj" {
		t.Errorf("CatalogDownloadSizeKey mmproj = %q, %v", key, err)
	}
	if key, _ := CatalogDownloadSizeKey("abc", FileKindMain); key != "abc" {
		t.Errorf("CatalogDownloadSizeKey main = %q", key)
	}
}

func TestDownloadStateCases(t *testing.T) {
	ctx := context.Background()
	_ = ctx

	// Legacy file with no recorded size and no catalog requirements: downloaded.
	legacy := newDownloadManager(t, "")
	legacyEntry := testEntry()
	legacyEntry.BaseWeightGb = 0
	writeSizedFile(t, legacy.ModelPath(legacyEntry), []byte("partial-or-legacy"))
	if !legacy.IsCatalogModelDownloaded(legacyEntry) {
		t.Error("legacy file without expectations should count as downloaded")
	}

	// Tiny file for an entry with a known weight: rejected.
	tiny := newDownloadManager(t, "")
	entry := testEntry()
	writeSizedFile(t, tiny.ModelPath(entry), make([]byte, 1024))
	if tiny.IsCatalogModelDownloaded(entry) {
		t.Error("1 KiB file for a 17 GB model should be rejected")
	}

	// Recorded expected size that differs: rejected, and reported partial.
	mismatch := newDownloadManager(t, "")
	key, _ := CatalogDownloadSizeKey(entry.ID, FileKindMain)
	const expectedBytes = int64(17_010_978_592)
	const partialBytes = int64(50_000_000)
	mismatch.sizes.Put(key, expectedBytes, entry.HFFile)
	writeSizedFile(t, mismatch.ModelPath(entry), make([]byte, partialBytes))
	state := mismatch.CatalogDownloadState(entry)
	if state.Status != StatusPartial {
		t.Errorf("status = %q, want partial", state.Status)
	}
	if state.DownloadedBytes != partialBytes || state.ExpectedBytes != expectedBytes {
		t.Errorf("state bytes = %d/%d", state.DownloadedBytes, state.ExpectedBytes)
	}
	if mismatch.IsCatalogModelDownloaded(entry) {
		t.Error("a partial file must never be reported as downloaded")
	}

	// A file matching the recorded expected size is accepted.
	match := newDownloadManager(t, "")
	match.sizes.Put(key, 4096, entry.HFFile)
	writeSizedFile(t, match.ModelPath(entry), make([]byte, 4096))
	if !match.IsCatalogModelDownloaded(entry) {
		t.Error("file matching the recorded expected size should be downloaded")
	}
}

func writeSizedFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// hfServer serves a single model file with HEAD and Range support, counting
// GET requests so idempotence can be asserted.
func hfServer(t *testing.T, entry CatalogEntry, body []byte, gets *int64) *httptest.Server {
	t.Helper()
	path := fmt.Sprintf("/%s/resolve/main/%s", entry.HFRepo, entry.HFFile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		total := int64(len(body))
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt64(gets, 1)
		start := int64(0)
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"), 10, 64)
			if err == nil && n >= 0 && n < total {
				start = n
			}
		}
		if start > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
			w.Header().Set("Content-Length", strconv.FormatInt(total-start, 10))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
			w.WriteHeader(http.StatusOK)
		}
		_, _ = w.Write(body[start:])
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDownloadModelResolvesThenNoOps(t *testing.T) {
	entry := testEntry()
	entry.BaseWeightGb = 0 // exact-size bookkeeping only
	body := bytes.Repeat([]byte("g"), 5000)
	var gets int64
	srv := hfServer(t, entry, body, &gets)
	m := newDownloadManager(t, srv.URL)
	ctx := context.Background()

	res, err := m.DownloadModel(ctx, entry, DownloadModelOptions{})
	if err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if res.Path != m.ModelPath(entry) {
		t.Fatalf("download path = %q, want %q", res.Path, m.ModelPath(entry))
	}
	if got := localFileSize(res.Path); got != int64(len(body)) {
		t.Fatalf("downloaded %d bytes, want %d", got, len(body))
	}
	if !m.IsCatalogModelDownloaded(entry) {
		t.Fatal("completed download should be reported as downloaded")
	}
	key, _ := CatalogDownloadSizeKey(entry.ID, FileKindMain)
	if rec, ok := m.sizes.Get(key); !ok || rec.ExpectedBytes != int64(len(body)) {
		t.Fatalf("expected size record = %+v, ok=%v", rec, ok)
	}
	if got := atomic.LoadInt64(&gets); got != 1 {
		t.Fatalf("first download made %d GETs, want 1", got)
	}

	// Second identical call: already complete, so no GET at all.
	again, err := m.DownloadModel(ctx, entry, DownloadModelOptions{})
	if err != nil {
		t.Fatalf("second DownloadModel: %v", err)
	}
	if !again.Skipped {
		t.Error("second DownloadModel should report Skipped")
	}
	if again.Path != res.Path {
		t.Errorf("second DownloadModel path = %q, want %q", again.Path, res.Path)
	}
	if got := atomic.LoadInt64(&gets); got != 1 {
		t.Fatalf("second DownloadModel made extra GETs (total %d), want 1", got)
	}
}

func TestDownloadModelResumesTruncatedFile(t *testing.T) {
	entry := testEntry()
	entry.BaseWeightGb = 0
	body := bytes.Repeat([]byte("g"), 5000)
	var gets int64
	srv := hfServer(t, entry, body, &gets)
	m := newDownloadManager(t, srv.URL)

	// A truncated file with a recorded expected size is partial, never complete.
	key, _ := CatalogDownloadSizeKey(entry.ID, FileKindMain)
	m.sizes.Put(key, int64(len(body)), entry.HFFile)
	writeSizedFile(t, m.ModelPath(entry), body[:2000])
	state := m.CatalogDownloadState(entry)
	if state.Status != StatusPartial {
		t.Fatalf("truncated file status = %q, want partial", state.Status)
	}
	if m.IsCatalogModelDownloaded(entry) {
		t.Fatal("truncated file must not be reported as downloaded")
	}

	res, err := m.DownloadModel(context.Background(), entry, DownloadModelOptions{})
	if err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if res.Skipped {
		t.Fatal("a partial download must not be skipped")
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("resumed file is %d bytes and does not match the source (%d bytes)", len(got), len(body))
	}
	if !m.IsCatalogModelDownloaded(entry) {
		t.Error("resumed download should now be complete")
	}
}
