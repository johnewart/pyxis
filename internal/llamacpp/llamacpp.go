/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// Package llamacpp owns managed llama.cpp provisioning inside llmd: resolving
// and upgrading the llama-server binary, and downloading GGUF models from the
// Hugging Face catalog. Every filesystem root and HTTP endpoint is injected so
// the package never touches the real home directory or the network in tests.
package llamacpp

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultReleaseTag is the llama.cpp release llmd pins by default.
const DefaultReleaseTag = "b9436"

// BinaryVersionFileName is the marker written next to the managed binary.
const BinaryVersionFileName = "llama-server.version"

// DefaultGitHubAPIBase is the GitHub API root used to resolve release assets.
const DefaultGitHubAPIBase = "https://api.github.com"

// DefaultHFBase is the Hugging Face root used to resolve model files.
const DefaultHFBase = "https://huggingface.co"

// ReleaseTagEnvVar overrides the pinned llama.cpp release tag.
const ReleaseTagEnvVar = "LLMD_LLAMA_CPP_RELEASE_TAG"

// BinaryName is the llama-server executable name for the current platform.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// Options configures a Manager. CacheDir and ModelsDir are required for any
// filesystem work; tests should pass t.TempDir() values.
type Options struct {
	// CacheDir is the managed llama-server install root.
	CacheDir string
	// ModelsDir is where GGUF model files are downloaded.
	ModelsDir string
	// HTTPClient performs all outbound requests.
	HTTPClient *http.Client
	// ReleaseTag pins the llama.cpp release; falls back to the env override,
	// then DefaultReleaseTag.
	ReleaseTag string
	// GitHubAPIBase is the release-metadata root (injected in tests).
	GitHubAPIBase string
	// HFBase is the Hugging Face resolve root (injected in tests).
	HFBase string
	// SizeStore persists expected download sizes. Defaults to a JSON file
	// under ModelsDir — llmd's own state, never a client's config DB.
	SizeStore DownloadSizeStore
	// HFToken authorizes gated Hugging Face repositories.
	HFToken string
}

// Manager provisions llama-server binaries and catalog models.
type Manager struct {
	cacheDir   string
	modelsDir  string
	httpClient *http.Client
	releaseTag string
	githubBase string
	hfBase     string
	sizes      DownloadSizeStore
	hfToken    string
}

// New builds a Manager from opts, filling in defaults for anything omitted.
func New(opts Options) *Manager {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	tag := strings.TrimSpace(opts.ReleaseTag)
	if tag == "" {
		tag = strings.TrimSpace(os.Getenv(ReleaseTagEnvVar))
	}
	if tag == "" {
		tag = DefaultReleaseTag
	}
	githubBase := strings.TrimRight(strings.TrimSpace(opts.GitHubAPIBase), "/")
	if githubBase == "" {
		githubBase = DefaultGitHubAPIBase
	}
	hfBase := strings.TrimRight(strings.TrimSpace(opts.HFBase), "/")
	if hfBase == "" {
		hfBase = DefaultHFBase
	}
	sizes := opts.SizeStore
	if sizes == nil {
		sizes = NewFileDownloadSizeStore(filepath.Join(opts.ModelsDir, ".llmd-download-sizes.json"))
	}
	return &Manager{
		cacheDir:   opts.CacheDir,
		modelsDir:  opts.ModelsDir,
		httpClient: client,
		releaseTag: tag,
		githubBase: githubBase,
		hfBase:     hfBase,
		sizes:      sizes,
		hfToken:    strings.TrimSpace(opts.HFToken),
	}
}

// CacheDir is the managed llama-server install root.
func (m *Manager) CacheDir() string { return m.cacheDir }

// ModelsDir is the GGUF download root.
func (m *Manager) ModelsDir() string { return m.modelsDir }

// ReleaseTag is the pinned llama.cpp release tag.
func (m *Manager) ReleaseTag() string { return m.releaseTag }

// ManagedBinaryPath is the expected managed llama-server path. It may not exist
// until the first successful EnsureBinary call.
func (m *Manager) ManagedBinaryPath() string {
	return filepath.Join(m.cacheDir, BinaryName())
}

// managedVersionPath is the release-tag marker beside the managed binary.
func (m *Manager) managedVersionPath() string {
	return filepath.Join(m.cacheDir, BinaryVersionFileName)
}
