/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// newBinaryManager builds a Manager rooted entirely in t.TempDir().
func newBinaryManager(t *testing.T, tag string, base string) *Manager {
	t.Helper()
	return New(Options{
		CacheDir:      filepath.Join(t.TempDir(), "llama-server"),
		ModelsDir:     t.TempDir(),
		ReleaseTag:    tag,
		GitHubAPIBase: base,
		HFBase:        base,
		SizeStore:     NewMemoryDownloadSizeStore(),
	})
}

func TestBinaryManagerUsesInjectedDirs(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "llama-server")
	m := New(Options{CacheDir: cacheDir, ModelsDir: t.TempDir(), ReleaseTag: "b9436"})

	got := m.ManagedBinaryPath()
	want := filepath.Join(cacheDir, BinaryName())
	if got != want {
		t.Fatalf("ManagedBinaryPath() = %q, want %q", got, want)
	}
	if strings.Contains(got, ".llmd") {
		t.Fatalf("managed binary path leaked the real home directory: %q", got)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(got, filepath.Join(home, ".llmd")) {
		t.Fatalf("managed binary path rooted in $HOME/.llmd: %q", got)
	}
	if m.ReleaseTag() != "b9436" {
		t.Fatalf("ReleaseTag() = %q, want b9436", m.ReleaseTag())
	}
}

func TestBinaryReleaseTagComparisons(t *testing.T) {
	equivalents := []struct {
		a, b string
		want bool
	}{
		{"b9436", "9436", true},
		{"9436", "b9436", true},
		{"b9436", "b9436", true},
		{"b9436", "b9400", false},
		{"", "b9436", false},
		{"main", "b9436", false},
	}
	for _, c := range equivalents {
		if got := ReleaseTagsEquivalent(c.a, c.b); got != c.want {
			t.Errorf("ReleaseTagsEquivalent(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}

	newer := []struct {
		installed, latest string
		want              bool
	}{
		{"b9400", "b9436", true},
		{"9400", "b9436", true},
		{"b9436", "b9436", false},
		{"b9500", "b9436", false},
		{"unknown", "b9436", false},
	}
	for _, c := range newer {
		if got := IsReleaseNewer(c.installed, c.latest); got != c.want {
			t.Errorf("IsReleaseNewer(%q,%q) = %v, want %v", c.installed, c.latest, got, c.want)
		}
	}

	if n, ok := ParseBuildNumber(" b9436 "); !ok || n != 9436 {
		t.Errorf("ParseBuildNumber(b9436) = %d,%v", n, ok)
	}
	if _, ok := ParseBuildNumber("b94x36"); ok {
		t.Error("ParseBuildNumber accepted a non-build tag")
	}
}

func TestBinaryAssetFileNameMatchesPlatform(t *testing.T) {
	name, err := PickReleaseAssetFileName("b9436")
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if err == nil {
			t.Fatal("expected an error on an unsupported platform")
		}
		return
	}
	if err != nil {
		t.Fatalf("PickReleaseAssetFileName: %v", err)
	}
	if !strings.HasPrefix(name, "llama-b9436-bin-") {
		t.Fatalf("asset name %q missing tag prefix", name)
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		if name != "llama-b9436-bin-ubuntu-x64.tar.gz" {
			t.Fatalf("asset name = %q, want llama-b9436-bin-ubuntu-x64.tar.gz", name)
		}
	}
}

func TestBinaryValidatePathRejectsMissingAndNonExecutable(t *testing.T) {
	dir := t.TempDir()

	if err := ValidateBinaryPath(""); err == nil {
		t.Error("empty path was accepted")
	}
	if err := ValidateBinaryPath(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing path was accepted")
	}

	nonExec := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(nonExec, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := ValidateBinaryPath(nonExec); err == nil {
			t.Error("non-executable path was accepted")
		}
		if err := os.Chmod(nonExec, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateBinaryPath(nonExec); err != nil {
		t.Errorf("executable path rejected: %v", err)
	}
}

// releaseServer serves a GitHub release payload and a tar.gz asset containing a
// llama-server binary, counting every request it handles.
func releaseServer(t *testing.T, tag string, requests *int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	assetName, err := PickReleaseAssetFileName(tag)
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	mux.HandleFunc("/repos/ggml-org/llama.cpp/releases/tags/"+tag, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(requests, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"assets":[{"name":%q,"browser_download_url":%q}]}`,
			assetName, srv.URL+"/asset/"+tag)
	})
	mux.HandleFunc("/asset/"+tag, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(requests, 1)
		_, _ = w.Write(tarGzWithBinary(t, tag))
	})
	return srv
}

// tarGzWithBinary builds a release archive holding build/bin/llama-server plus a
// co-located runtime library.
func tarGzWithBinary(t *testing.T, tag string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	write := func(name, body string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("build/bin/"+BinaryName(), "#!/bin/sh\necho version: "+tag+"\n", 0o755)
	write("build/bin/libggml.so", "lib-"+tag, 0o644)

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBinaryEnsureInstallsThenNoOpsAndUpgrades(t *testing.T) {
	var requests int64
	tag := "b9436"
	srv := releaseServer(t, tag, &requests)
	m := newBinaryManager(t, tag, srv.URL)

	ctx := context.Background()
	path, err := m.EnsureBinary(ctx, EnsureBinaryOptions{})
	if err != nil {
		t.Fatalf("EnsureBinary: %v", err)
	}
	if path != m.ManagedBinaryPath() {
		t.Fatalf("EnsureBinary returned %q, want %q", path, m.ManagedBinaryPath())
	}
	if !isRegularFile(path) {
		t.Fatalf("binary was not installed at %q", path)
	}
	if got := m.readCachedVersion(); got != tag {
		t.Fatalf("version marker = %q, want %q", got, tag)
	}
	if !isRegularFile(filepath.Join(m.CacheDir(), "libggml.so")) {
		t.Error("co-located runtime library was not installed")
	}
	if runtime.GOOS != "windows" {
		if err := ValidateBinaryPath(path); err != nil {
			t.Errorf("installed binary is not executable: %v", err)
		}
	}
	firstRequests := atomic.LoadInt64(&requests)
	if firstRequests == 0 {
		t.Fatal("expected the first EnsureBinary to hit the release server")
	}

	// Second identical call must be a pure no-op: zero HTTP requests.
	again, err := m.EnsureBinary(ctx, EnsureBinaryOptions{})
	if err != nil {
		t.Fatalf("second EnsureBinary: %v", err)
	}
	if again != path {
		t.Fatalf("second EnsureBinary returned %q, want %q", again, path)
	}
	if got := atomic.LoadInt64(&requests); got != firstRequests {
		t.Fatalf("second EnsureBinary made %d extra HTTP requests, want 0", got-firstRequests)
	}

	info := m.VersionInfo()
	if info.InstalledVersion != tag || info.UpdateAvailable {
		t.Fatalf("VersionInfo = %+v, want installed %s and no update", info, tag)
	}

	// A newer pinned tag re-downloads and rewrites the marker into the same cache.
	newTag := "b9500"
	var upgradeRequests int64
	upgradeSrv := releaseServer(t, newTag, &upgradeRequests)
	upgraded := New(Options{
		CacheDir:      m.CacheDir(),
		ModelsDir:     m.ModelsDir(),
		ReleaseTag:    newTag,
		GitHubAPIBase: upgradeSrv.URL,
		SizeStore:     NewMemoryDownloadSizeStore(),
	})
	if upgraded.IsCachedBinaryCurrent() {
		t.Fatal("cache should be stale against the newer tag")
	}
	if _, err := upgraded.UpgradeBinary(ctx); err != nil {
		t.Fatalf("UpgradeBinary: %v", err)
	}
	if atomic.LoadInt64(&upgradeRequests) == 0 {
		t.Fatal("upgrade did not re-download")
	}
	if got := upgraded.readCachedVersion(); got != newTag {
		t.Fatalf("version marker after upgrade = %q, want %q", got, newTag)
	}
}
