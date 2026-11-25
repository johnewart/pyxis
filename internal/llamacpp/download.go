/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CatalogEntry is the subset of a llama.cpp catalog preset the download path
// needs. Callers supply it; llmd does not own the curated catalog yet.
type CatalogEntry struct {
	// ID is the catalog preset id (e.g. gemma-4-26b-a4b-it).
	ID string
	// HFRepo is the Hugging Face repository.
	HFRepo string
	// HFFile is the GGUF file name within the repository.
	HFFile string
	// HFRevision is the git revision; defaults to "main".
	HFRevision string
	// BaseWeightGb is the curated weight used for size heuristics; 0 unknown.
	BaseWeightGb float64
}

// CatalogDownloadState describes a catalog preset's primary GGUF on disk.
type CatalogDownloadState struct {
	// Status is missing, partial or complete.
	Status DownloadFileStatus
	// DownloadedBytes is the current on-disk size.
	DownloadedBytes int64
	// ExpectedBytes is the recorded or estimated full size; 0 when unknown.
	ExpectedBytes int64
	// Path is where the model file lives (or would live).
	Path string
}

var contentRangeTotalRe = regexp.MustCompile(`/(\d+)\s*$`)

// localFileSize returns the size of path, or 0 when it is absent.
func localFileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return 0
	}
	return st.Size()
}

// ModelPath is where a catalog entry's primary GGUF lives under ModelsDir.
func (m *Manager) ModelPath(entry CatalogEntry) string {
	return filepath.Join(m.modelsDir, entry.HFFile)
}

// recordedExpectedBytes returns the persisted expected size for the entry's
// primary file, or 0 when none has been recorded.
func (m *Manager) recordedExpectedBytes(entry CatalogEntry) int64 {
	key, err := CatalogDownloadSizeKey(entry.ID, FileKindMain)
	if err != nil {
		return 0
	}
	rec, ok := m.sizes.Get(key)
	if !ok {
		return 0
	}
	return rec.ExpectedBytes
}

// CatalogDownloadState reports whether a catalog preset's primary GGUF is
// missing, partially downloaded, or complete.
func (m *Manager) CatalogDownloadState(entry CatalogEntry) CatalogDownloadState {
	path := m.ModelPath(entry)
	state := CatalogDownloadState{Status: StatusMissing, Path: path}
	if strings.TrimSpace(m.modelsDir) == "" || strings.TrimSpace(entry.HFFile) == "" {
		return state
	}
	expected := m.recordedExpectedBytes(entry)
	if expected <= 0 {
		expected = CatalogBytesFromWeightGb(entry.BaseWeightGb)
	}
	downloaded := localFileSize(path)
	state.DownloadedBytes = downloaded
	state.ExpectedBytes = expected
	state.Status = ClassifyCatalogDownloadFile(
		downloaded,
		expected,
		CatalogMinCompleteBytes(entry.BaseWeightGb),
	)
	return state
}

// IsCatalogModelDownloaded reports whether the preset's primary GGUF is
// complete on disk. A partial file is never reported as downloaded.
func (m *Manager) IsCatalogModelDownloaded(entry CatalogEntry) bool {
	return m.CatalogDownloadState(entry).Status == StatusComplete
}

// DownloadProgress is reported while a model file is being fetched.
type DownloadProgress struct {
	// DownloadedBytes is the running byte count, including resumed bytes.
	DownloadedBytes int64
	// TotalBytes is the expected total; 0 when unknown.
	TotalBytes int64
	// FileLabel names the file being downloaded.
	FileLabel string
}

// DownloadResult is the outcome of a successful DownloadModel call.
type DownloadResult struct {
	// Path is the on-disk model file.
	Path string
	// TotalBytes is the final expected size.
	TotalBytes int64
	// Skipped is true when the file was already complete and nothing was
	// fetched.
	Skipped bool
}

// DownloadModelOptions tunes DownloadModel.
type DownloadModelOptions struct {
	// OnProgress, when set, receives periodic progress updates.
	OnProgress func(DownloadProgress)
}

// resolveURL builds the Hugging Face resolve URL for a catalog entry.
func (m *Manager) resolveURL(entry CatalogEntry) string {
	revision := strings.TrimSpace(entry.HFRevision)
	if revision == "" {
		revision = "main"
	}
	return fmt.Sprintf("%s/%s/resolve/%s/%s", m.hfBase, entry.HFRepo, revision, entry.HFFile)
}

// applyAuth attaches the Hugging Face token when one is configured.
func (m *Manager) applyAuth(req *http.Request) {
	if m.hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.hfToken)
	}
}

// remoteContentLength probes the file size with a HEAD request. Returns 0 when
// the size cannot be determined.
func (m *Manager) remoteContentLength(ctx context.Context, entry CatalogEntry) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, m.resolveURL(entry), nil)
	if err != nil {
		return 0
	}
	m.applyAuth(req)
	res, err := m.httpClient.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return 0
	}
	return parseInt64(res.Header.Get("Content-Length"))
}

func parseInt64(raw string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseContentRangeTotal extracts the total size from a Content-Range header.
func parseContentRangeTotal(header string) int64 {
	m := contentRangeTotalRe.FindStringSubmatch(strings.TrimSpace(header))
	if m == nil {
		return 0
	}
	return parseInt64(m[1])
}

// CanResumePartialDownload reports whether an existing partial file can be
// continued with a ranged request.
func CanResumePartialDownload(existingBytes, expectedBytes int64) bool {
	return existingBytes > 0 &&
		expectedBytes > 0 &&
		existingBytes < expectedBytes &&
		!IsDownloadFileSizeValid(existingBytes, expectedBytes)
}

// removeIncompleteLocalFile deletes a file that does not match the expected
// size, so a fresh download never appends onto stale bytes.
func removeIncompleteLocalFile(path string, expectedBytes int64) {
	actual := localFileSize(path)
	if actual == 0 {
		return
	}
	if expectedBytes > 0 && !IsDownloadFileSizeValid(actual, expectedBytes) {
		_ = os.Remove(path)
	}
}

// DownloadModel fetches a catalog entry's GGUF into ModelsDir, resuming a
// partial file where possible. It is idempotent: when the file on disk is
// already complete it issues no GET and returns Skipped.
func (m *Manager) DownloadModel(ctx context.Context, entry CatalogEntry, opts DownloadModelOptions) (DownloadResult, error) {
	if strings.TrimSpace(m.modelsDir) == "" {
		return DownloadResult{}, fmt.Errorf("llamacpp: ModelsDir is not configured")
	}
	if strings.TrimSpace(entry.HFFile) == "" {
		return DownloadResult{}, fmt.Errorf("no file specified; catalog entries must provide hfFile")
	}

	outPath := m.ModelPath(entry)
	if state := m.CatalogDownloadState(entry); state.Status == StatusComplete {
		return DownloadResult{Path: outPath, TotalBytes: state.DownloadedBytes, Skipped: true}, nil
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return DownloadResult{}, fmt.Errorf("failed to create directory: %w", err)
	}

	expectedBytes := m.remoteContentLength(ctx, entry)
	if expectedBytes <= 0 {
		expectedBytes = m.recordedExpectedBytes(entry)
	}

	existingBytes := localFileSize(outPath)
	resumeFrom := int64(0)
	if CanResumePartialDownload(existingBytes, expectedBytes) {
		resumeFrom = existingBytes
	} else {
		removeIncompleteLocalFile(outPath, expectedBytes)
	}

	res, err := m.fetchWithOptionalRange(ctx, entry, resumeFrom)
	if err != nil {
		return DownloadResult{}, err
	}
	if resumeFrom > 0 && (res.StatusCode == http.StatusRequestedRangeNotSatisfiable ||
		res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusForbidden) {
		res.Body.Close()
		_ = os.Remove(outPath)
		resumeFrom = 0
		if res, err = m.fetchWithOptionalRange(ctx, entry, 0); err != nil {
			return DownloadResult{}, err
		}
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return DownloadResult{}, describeDownloadError(res, entry)
	}

	if total := parseContentRangeTotal(res.Header.Get("Content-Range")); total > 0 {
		expectedBytes = total
	} else if chunk := parseInt64(res.Header.Get("Content-Length")); chunk > 0 {
		expectedBytes = resumeFrom + chunk
	}

	if expectedBytes > 0 {
		if key, keyErr := CatalogDownloadSizeKey(entry.ID, FileKindMain); keyErr == nil {
			m.sizes.Put(key, expectedBytes, entry.HFFile)
		}
	}

	if resumeFrom == 0 {
		removeIncompleteLocalFile(outPath, expectedBytes)
	}

	written, err := m.streamToFile(res.Body, outPath, resumeFrom, expectedBytes, entry, opts)
	if err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{Path: outPath, TotalBytes: written}, nil
}

// fetchWithOptionalRange GETs the model file, resuming from rangeStart when > 0.
func (m *Manager) fetchWithOptionalRange(ctx context.Context, entry CatalogEntry, rangeStart int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.resolveURL(entry), nil)
	if err != nil {
		return nil, err
	}
	m.applyAuth(req)
	if rangeStart > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", rangeStart))
	}
	return m.httpClient.Do(req)
}

// describeDownloadError turns a non-2xx response into an actionable error.
func describeDownloadError(res *http.Response, entry CatalogEntry) error {
	switch res.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("unauthorized: add a Hugging Face token for gated repositories")
	case http.StatusNotFound:
		return fmt.Errorf("file not found: %s in %s", entry.HFFile, entry.HFRepo)
	default:
		body, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
}

// streamToFile writes the response body to outPath, appending when resuming,
// and reports progress. It returns the final on-disk size.
func (m *Manager) streamToFile(
	body io.Reader,
	outPath string,
	resumeFrom int64,
	totalBytes int64,
	entry CatalogEntry,
	opts DownloadModelOptions,
) (int64, error) {
	flags := os.O_CREATE | os.O_WRONLY
	if resumeFrom > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(outPath, flags, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	downloaded := resumeFrom
	buf := make([]byte, 1<<20)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return downloaded, writeErr
			}
			downloaded += int64(n)
			if opts.OnProgress != nil {
				opts.OnProgress(DownloadProgress{
					DownloadedBytes: downloaded,
					TotalBytes:      totalBytes,
					FileLabel:       entry.HFFile,
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return downloaded, readErr
		}
	}
	return downloaded, nil
}

// ListLocalModels returns the GGUF model file stems found in ModelsDir, looking
// one level down into immediate subdirectories.
func (m *Manager) ListLocalModels() []string {
	if strings.TrimSpace(m.modelsDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(m.modelsDir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if !seen[stem] {
			seen[stem] = true
			out = append(out, stem)
		}
	}
	for _, e := range entries {
		if !e.IsDir() {
			if strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
				add(e.Name())
			}
			continue
		}
		sub, err := os.ReadDir(filepath.Join(m.modelsDir, e.Name()))
		if err != nil {
			continue
		}
		for _, s := range sub {
			if !s.IsDir() && strings.HasSuffix(strings.ToLower(s.Name()), ".gguf") {
				add(s.Name())
				break
			}
		}
	}
	return out
}
