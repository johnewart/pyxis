/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DownloadFileKind distinguishes a catalog entry's primary weights from its
// optional multimodal projector.
type DownloadFileKind string

const (
	// FileKindMain is the primary GGUF weights file.
	FileKindMain DownloadFileKind = "main"
	// FileKindMmproj is the multimodal projector file.
	FileKindMmproj DownloadFileKind = "mmproj"
)

// DownloadFileStatus classifies an on-disk download.
type DownloadFileStatus string

const (
	// StatusMissing means nothing usable is on disk.
	StatusMissing DownloadFileStatus = "missing"
	// StatusPartial means bytes exist but the file is incomplete.
	StatusPartial DownloadFileStatus = "partial"
	// StatusComplete means the file is believed whole.
	StatusComplete DownloadFileStatus = "complete"
)

// CatalogDownloadSizeKey is the stable key for a persisted expected download
// size (catalog preset + file role). It mirrors catalogDownloadSizeKey in TS.
func CatalogDownloadSizeKey(catalogID string, kind DownloadFileKind) (string, error) {
	id := strings.TrimSpace(catalogID)
	if id == "" {
		return "", errors.New("CatalogDownloadSizeKey requires catalogId")
	}
	if kind == FileKindMmproj {
		return id + "::mmproj", nil
	}
	return id, nil
}

// IsDownloadFileSizeValid reports whether an on-disk file exactly matches the
// expected Hugging Face size.
func IsDownloadFileSizeValid(actualBytes, expectedBytes int64) bool {
	if actualBytes <= 0 || expectedBytes <= 0 {
		return false
	}
	return actualBytes == expectedBytes
}

// CatalogBytesFromWeightGb estimates full file bytes from the curated catalog
// weight. Returns 0 when no estimate is available.
func CatalogBytesFromWeightGb(baseWeightGb float64) int64 {
	if baseWeightGb <= 0 || math.IsNaN(baseWeightGb) || math.IsInf(baseWeightGb, 0) {
		return 0
	}
	return int64(math.Round(baseWeightGb * 1_000_000_000))
}

// CatalogMinCompleteBytes is the floor below which a file cannot be complete
// when no exact expected size is known: 50% of the catalog weight, never less
// than 1 MB. Returns 0 when the weight is unknown.
func CatalogMinCompleteBytes(baseWeightGb float64) int64 {
	if baseWeightGb <= 0 || math.IsNaN(baseWeightGb) || math.IsInf(baseWeightGb, 0) {
		return 0
	}
	est := int64(math.Floor(baseWeightGb * 0.5 * 1_000_000_000))
	if est < 1_000_000 {
		return 1_000_000
	}
	return est
}

// ClassifyCatalogDownloadFile mirrors classifyCatalogDownloadFile in TS.
// expectedBytes / minCompleteBytes of 0 mean "unknown".
func ClassifyCatalogDownloadFile(downloadedBytes, expectedBytes, minCompleteBytes int64) DownloadFileStatus {
	if downloadedBytes <= 0 {
		return StatusMissing
	}
	if expectedBytes > 0 && IsDownloadFileSizeValid(downloadedBytes, expectedBytes) {
		return StatusComplete
	}
	if expectedBytes > 0 && downloadedBytes < expectedBytes {
		return StatusPartial
	}
	if minCompleteBytes > 0 && downloadedBytes < minCompleteBytes {
		return StatusPartial
	}
	return StatusComplete
}

// DownloadSizeRecord is a persisted expected download size.
type DownloadSizeRecord struct {
	ExpectedBytes int64  `json:"expectedBytes"`
	HFFile        string `json:"hfFile"`
	CachedAt      string `json:"cachedAt"`
}

// DownloadSizeStore persists expected download sizes. llmd owns this state; it
// must never read a client's config database.
type DownloadSizeStore interface {
	// Get returns the record for key, or ok=false when absent.
	Get(key string) (DownloadSizeRecord, bool)
	// Put records the expected byte size for key.
	Put(key string, expectedBytes int64, hfFile string)
}

// fileDownloadSizeStore keeps records in a small JSON file owned by llmd.
type fileDownloadSizeStore struct {
	mu   sync.Mutex
	path string
}

// NewFileDownloadSizeStore returns a DownloadSizeStore backed by a JSON file.
func NewFileDownloadSizeStore(path string) DownloadSizeStore {
	return &fileDownloadSizeStore{path: path}
}

func (s *fileDownloadSizeStore) load() map[string]DownloadSizeRecord {
	out := map[string]DownloadSizeRecord{}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]DownloadSizeRecord{}
	}
	return out
}

func (s *fileDownloadSizeStore) Get(key string) (DownloadSizeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.load()[key]
	return rec, ok
}

func (s *fileDownloadSizeStore) Put(key string, expectedBytes int64, hfFile string) {
	if expectedBytes <= 0 || strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.load()
	records[key] = DownloadSizeRecord{
		ExpectedBytes: expectedBytes,
		HFFile:        hfFile,
		CachedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(records)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o644)
}

// MemoryDownloadSizeStore is an in-memory DownloadSizeStore for tests.
type MemoryDownloadSizeStore struct {
	mu      sync.Mutex
	records map[string]DownloadSizeRecord
}

// NewMemoryDownloadSizeStore returns an empty in-memory store.
func NewMemoryDownloadSizeStore() *MemoryDownloadSizeStore {
	return &MemoryDownloadSizeStore{records: map[string]DownloadSizeRecord{}}
}

// Get returns the record for key.
func (s *MemoryDownloadSizeStore) Get(key string) (DownloadSizeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key]
	return rec, ok
}

// Put records the expected byte size for key.
func (s *MemoryDownloadSizeStore) Put(key string, expectedBytes int64, hfFile string) {
	if expectedBytes <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[key] = DownloadSizeRecord{
		ExpectedBytes: expectedBytes,
		HFFile:        hfFile,
		CachedAt:      time.Now().UTC().Format(time.RFC3339),
	}
}
