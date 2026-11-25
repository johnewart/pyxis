/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var buildNumberRe = regexp.MustCompile(`^[bB]?(\d+)$`)
var bTagRe = regexp.MustCompile(`(?i)\bb(\d+)\b`)
var versionLineRe = regexp.MustCompile(`(?i)version:\s*(\d+)`)

// ParseBuildNumber parses llama.cpp release tags like b9436 or bare build
// numbers. ok is false when the tag is not a build number.
func ParseBuildNumber(tag string) (int, bool) {
	m := buildNumberRe.FindStringSubmatch(strings.TrimSpace(tag))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// IsReleaseNewer reports whether the installed build is older than latest.
func IsReleaseNewer(installed, latest string) bool {
	i, iOK := ParseBuildNumber(installed)
	l, lOK := ParseBuildNumber(latest)
	if !iOK || !lOK {
		return false
	}
	return i < l
}

// ReleaseTagsEquivalent reports whether two tags name the same llama.cpp build
// (b9436 and 9436 are the same build).
func ReleaseTagsEquivalent(a, b string) bool {
	left := strings.TrimSpace(a)
	right := strings.TrimSpace(b)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	ab, aOK := ParseBuildNumber(left)
	bb, bOK := ParseBuildNumber(right)
	return aOK && bOK && ab == bb
}

// releaseArch maps GOARCH onto llama.cpp's asset naming.
func releaseArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

// PickReleaseAssetFileName returns the CPU-build release asset file name for
// the current OS/arch.
func PickReleaseAssetFileName(tag string) (string, error) {
	t := strings.TrimSpace(tag)
	arch := releaseArch()
	switch runtime.GOOS {
	case "darwin":
		if arch == "arm64" {
			return fmt.Sprintf("llama-%s-bin-macos-arm64.tar.gz", t), nil
		}
		return fmt.Sprintf("llama-%s-bin-macos-x64.tar.gz", t), nil
	case "linux":
		if arch == "arm64" {
			return fmt.Sprintf("llama-%s-bin-ubuntu-arm64.tar.gz", t), nil
		}
		return fmt.Sprintf("llama-%s-bin-ubuntu-x64.tar.gz", t), nil
	case "windows":
		if arch == "arm64" {
			return fmt.Sprintf("llama-%s-bin-win-cpu-arm64.zip", t), nil
		}
		return fmt.Sprintf("llama-%s-bin-win-cpu-x64.zip", t), nil
	default:
		return "", fmt.Errorf("unsupported platform for llama.cpp: %s", runtime.GOOS)
	}
}

// NormalizeBinaryPath resolves symlinks for comparison, falling back to an
// absolute path when the target does not exist.
func NormalizeBinaryPath(executablePath string) string {
	trimmed := strings.TrimSpace(executablePath)
	if trimmed == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		return resolved
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return trimmed
	}
	return abs
}

// BinaryPathsEquivalent reports whether two paths name the same binary.
func BinaryPathsEquivalent(a, b string) bool {
	left := NormalizeBinaryPath(a)
	right := NormalizeBinaryPath(b)
	return left != "" && right != "" && left == right
}

// ValidateBinaryPath returns an error when the path is missing or not
// executable — cases where spawning would fail with ENOENT/EACCES.
func ValidateBinaryPath(binaryPath string) error {
	trimmed := strings.TrimSpace(binaryPath)
	if trimmed == "" {
		return fmt.Errorf("llama-server binary is not configured")
	}
	st, err := os.Stat(trimmed)
	if err != nil || st.IsDir() {
		return fmt.Errorf("llama-server binary not found: %s", trimmed)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("llama-server binary is not executable: %s", trimmed)
	}
	return nil
}

// isRegularFile reports whether path exists as a regular file.
func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// normalizeDetectedVersion turns `llama-server --version` output into a tag.
func normalizeDetectedVersion(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if m := bTagRe.FindStringSubmatch(trimmed); m != nil {
		return "b" + m[1]
	}
	if m := versionLineRe.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	if n, ok := ParseBuildNumber(trimmed); ok {
		if strings.HasPrefix(strings.ToLower(trimmed), "b") {
			return trimmed
		}
		return strconv.Itoa(n)
	}
	return strings.TrimSpace(strings.SplitN(trimmed, "\n", 2)[0])
}

// DetectBinaryVersion returns a best-effort version string from a llama-server
// binary via `--version`, or "" when it cannot be determined.
func DetectBinaryVersion(binaryPath string) string {
	if !isRegularFile(binaryPath) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binaryPath, "--version").CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return normalizeDetectedVersion(string(out))
}

// VersionInfo describes the managed llama-server install.
type VersionInfo struct {
	// InstalledVersion is the tag on disk, or "" when nothing is installed.
	InstalledVersion string
	// LatestVersion is the release tag this Manager pins.
	LatestVersion string
	// UpdateAvailable is true when an upgrade would change the install.
	UpdateAvailable bool
	// Source is "cache" or "unknown".
	Source string
	// ManagedInstallDir is the managed install root.
	ManagedInstallDir string
	// ManagedBinaryPath is the expected managed binary path.
	ManagedBinaryPath string
}

// readCachedVersion reads the release-tag marker beside the managed binary.
func (m *Manager) readCachedVersion() string {
	data, err := os.ReadFile(m.managedVersionPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// installedNeedsUpdate mirrors installedNeedsUpdate in TS.
func (m *Manager) installedNeedsUpdate(installed string) bool {
	if installed == "" {
		return true
	}
	if installed == m.releaseTag {
		return false
	}
	return IsReleaseNewer(installed, m.releaseTag)
}

// VersionInfo reports the installed vs pinned llama-server versions.
func (m *Manager) VersionInfo() VersionInfo {
	info := VersionInfo{
		LatestVersion:     m.releaseTag,
		ManagedInstallDir: m.cacheDir,
		ManagedBinaryPath: m.ManagedBinaryPath(),
		Source:            "unknown",
		UpdateAvailable:   true,
	}
	bin := m.ManagedBinaryPath()
	if !isRegularFile(bin) {
		return info
	}
	installed := m.readCachedVersion()
	if installed == "" {
		installed = DetectBinaryVersion(bin)
	}
	info.InstalledVersion = installed
	info.Source = "cache"
	info.UpdateAvailable = m.installedNeedsUpdate(installed)
	return info
}

// IsCachedBinaryCurrent reports whether the managed cache already holds the
// pinned release.
func (m *Manager) IsCachedBinaryCurrent() bool {
	if !isRegularFile(m.ManagedBinaryPath()) {
		return false
	}
	return ReleaseTagsEquivalent(m.readCachedVersion(), m.releaseTag)
}

// EnsureBinaryOptions tunes EnsureBinary.
type EnsureBinaryOptions struct {
	// ForceUpdate re-downloads even when the cached binary is current.
	ForceUpdate bool
}

// EnsureBinary resolves the managed llama-server binary, downloading and
// installing the pinned release when the cache is missing or stale. It is
// idempotent: when the cache is already current it performs no HTTP request.
func (m *Manager) EnsureBinary(ctx context.Context, opts EnsureBinaryOptions) (string, error) {
	if strings.TrimSpace(m.cacheDir) == "" {
		return "", fmt.Errorf("llamacpp: CacheDir is not configured")
	}
	if !opts.ForceUpdate && m.IsCachedBinaryCurrent() {
		return m.ManagedBinaryPath(), nil
	}
	return m.downloadReleaseToCache(ctx, m.releaseTag)
}

// UpgradeBinary force-downloads the pinned release into the managed cache.
func (m *Manager) UpgradeBinary(ctx context.Context) (string, error) {
	return m.EnsureBinary(ctx, EnsureBinaryOptions{ForceUpdate: true})
}

// releaseAsset is the subset of the GitHub release payload we consume.
type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// fetchReleaseAssetURL resolves the download URL for this platform's asset.
func (m *Manager) fetchReleaseAssetURL(ctx context.Context, tag string) (string, error) {
	assetName, err := PickReleaseAssetFileName(tag)
	if err != nil {
		return "", err
	}
	apiURL := fmt.Sprintf("%s/repos/ggml-org/llama.cpp/releases/tags/%s", m.githubBase, url.PathEscape(tag))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("could not load llama.cpp release %s (HTTP %d)", tag, res.StatusCode)
	}
	var payload struct {
		Assets []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("could not parse llama.cpp release %s: %w", tag, err)
	}
	for _, a := range payload.Assets {
		if a.Name == assetName && strings.TrimSpace(a.URL) != "" {
			return strings.TrimSpace(a.URL), nil
		}
	}
	return "", fmt.Errorf("release %s has no asset %s for this platform", tag, assetName)
}

// downloadReleaseToCache downloads, extracts and installs the release bundle.
func (m *Manager) downloadReleaseToCache(ctx context.Context, tag string) (string, error) {
	assetName, err := PickReleaseAssetFileName(tag)
	if err != nil {
		return "", err
	}
	assetURL, err := m.fetchReleaseAssetURL(ctx, tag)
	if err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "llmd-llama-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, assetName)
	if err := m.downloadToFile(ctx, assetURL, archivePath); err != nil {
		return "", err
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := extractArchive(archivePath, extractDir); err != nil {
		return "", err
	}
	found := findBinaryInTree(extractDir, 0)
	if found == "" {
		return "", fmt.Errorf("downloaded archive did not contain %s", BinaryName())
	}

	destBin, err := InstallBundleFromExtract(found, m.cacheDir)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(m.managedVersionPath(), []byte(tag+"\n"), 0o644); err != nil {
		return "", err
	}
	return destBin, nil
}

// downloadToFile streams a URL into destPath.
func (m *Manager) downloadToFile(ctx context.Context, rawURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	res, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("download failed (HTTP %d)", res.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, res.Body)
	return err
}

// InstallBundleFromExtract copies llama-server and its co-located runtime libs
// from the extracted bundle into cacheDir, replacing any previous install.
func InstallBundleFromExtract(extractedBinaryPath, cacheDir string) (string, error) {
	sourceDir := filepath.Dir(extractedBinaryPath)
	destBin := filepath.Join(cacheDir, BinaryName())

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	if err := clearDirectoryContents(cacheDir); err != nil {
		return "", err
	}

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return "", fmt.Errorf("could not read llama-server bundle directory: %s", sourceDir)
	}
	for _, entry := range entries {
		src := filepath.Join(sourceDir, entry.Name())
		dest := filepath.Join(cacheDir, entry.Name())
		if entry.IsDir() {
			if err := copyTree(src, dest); err != nil {
				return "", err
			}
			continue
		}
		if err := copyFile(src, dest); err != nil {
			return "", err
		}
		if entry.Name() == BinaryName() && runtime.GOOS != "windows" {
			_ = os.Chmod(dest, 0o755)
		}
	}
	if !isRegularFile(destBin) {
		return "", fmt.Errorf("downloaded archive did not install %s", BinaryName())
	}
	return destBin, nil
}

// clearDirectoryContents removes everything inside dir, keeping dir itself.
func clearDirectoryContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies a single regular file preserving its mode.
func copyFile(src, dest string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// copyTree recursively copies a directory.
func copyTree(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

// findBinaryInTree locates llama-server within an extracted archive.
func findBinaryInTree(root string, depth int) string {
	if depth > 4 {
		return ""
	}
	direct := filepath.Join(root, BinaryName())
	if isRegularFile(direct) {
		return direct
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		full := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			if nested := findBinaryInTree(full, depth+1); nested != "" {
				return nested
			}
		}
	}
	return ""
}

// extractArchive expands a .zip or .tar.gz bundle into destDir.
func extractArchive(archivePath, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, destDir)
	}
	return extractTarGz(archivePath, destDir)
}

// safeJoin rejects archive entries that would escape destDir (zip-slip).
func safeJoin(destDir, name string) (string, error) {
	target := filepath.Join(destDir, filepath.Clean("/"+name))
	if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

func extractZip(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, entry := range zr.File {
		target, err := safeJoin(destDir, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, entry.Mode().Perm())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
