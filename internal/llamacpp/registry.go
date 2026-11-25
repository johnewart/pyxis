/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// Registry owns every llama-server child llmd spawns: it persists a minimal
// record of them to <stateDir>/llama-server/processes.json so a later run can
// reap orphans, and terminates them (SIGTERM, then SIGKILL after a bounded
// grace period) on shutdown.
//
// Invariants worth keeping:
//   - No package-level state. The activeByPid map and the startup-reap flag
//     live on a Registry instance whose state dir is injected, so tests never
//     touch a real HOME.
//   - processes.json is written only when membership changes, never on a
//     health tick.
//   - Kill escalation is a bounded context/ticker wait, never a busy-wait.

package llamacpp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RegistryRelPath is the processes file location under the registry state dir.
const RegistryRelPath = "llama-server/processes.json"

// DefaultTerminateTimeout bounds the SIGTERM->SIGKILL escalation.
const DefaultTerminateTimeout = 8 * time.Second

// ManagedRecord is one llama-server process tracked across runs.
type ManagedRecord struct {
	Pid       int       `json:"pid"`
	Port      int       `json:"port,omitempty"`
	ModelsDir string    `json:"modelsDir,omitempty"`
	CacheKey  string    `json:"cacheKey,omitempty"`
	StartedAt time.Time `json:"startedAt"`
}

type processRegistryFile struct {
	Processes []ManagedRecord `json:"processes"`
}

// SpawnSpec describes the llama-server child EnsureStarted should run.
type SpawnSpec struct {
	// CacheKey identifies the model/runtime configuration. One live child per key.
	CacheKey string
	// BinaryPath is the resolved llama-server executable (absolute).
	BinaryPath string
	// Args are passed to the binary verbatim.
	Args []string
	// Env, when non-nil, replaces the child environment.
	Env []string
	// ModelsDir and Port are recorded for diagnostics and orphan reaping.
	ModelsDir string
	Port      int
}

type managedChild struct {
	cmd    *exec.Cmd
	record ManagedRecord
	exited chan struct{}
	stderr *tailBuffer

	mu       sync.Mutex
	exitCode *int
}

// Pid is the OS pid of the child.
func (c *managedChild) Pid() int { return c.record.Pid }

// ExitCode reports the child's exit status, or nil while it is still running.
// It is the Go equivalent of node's ChildProcess.exitCode.
func (c *managedChild) ExitCode() *int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exitCode == nil {
		return nil
	}
	code := *c.exitCode
	return &code
}

// StderrTail returns the bounded tail of the child's stderr.
func (c *managedChild) StderrTail() string {
	if c.stderr == nil {
		return ""
	}
	return c.stderr.String()
}

// RegistryOptions configures a Registry. StateDir is required.
type RegistryOptions struct {
	// StateDir is llmd's own state root; processes.json lives beneath it.
	StateDir string
	// TerminateTimeout bounds SIGTERM->SIGKILL escalation.
	TerminateTimeout time.Duration
}

// Registry owns llmd's managed llama-server children.
type Registry struct {
	stateDir  string
	killGrace time.Duration

	mu         sync.Mutex
	children   map[string]*managedChild
	reapedOnce bool
}

// NewRegistry builds a Registry over the given state dir.
func NewRegistry(opts RegistryOptions) *Registry {
	grace := opts.TerminateTimeout
	if grace <= 0 {
		grace = DefaultTerminateTimeout
	}
	return &Registry{
		stateDir:  opts.StateDir,
		killGrace: grace,
		children:  map[string]*managedChild{},
	}
}

// FilePath is the processes.json path this Registry reads and writes.
func (r *Registry) FilePath() string {
	return filepath.Join(r.stateDir, filepath.FromSlash(RegistryRelPath))
}

// ReadRecords returns the persisted records. A missing or corrupt file yields
// an empty slice, never an error — the file is a best-effort hint.
func (r *Registry) ReadRecords() []ManagedRecord {
	raw, err := os.ReadFile(r.FilePath())
	if err != nil {
		return nil
	}
	var parsed processRegistryFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	return parsed.Processes
}

// WriteRecords persists records, creating the parent directory as needed.
func (r *Registry) WriteRecords(records []ManagedRecord) error {
	if records == nil {
		records = []ManagedRecord{}
	}
	path := r.FilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	raw, err := json.MarshalIndent(processRegistryFile{Processes: records}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

// syncLocked rewrites processes.json from the in-memory children. Callers hold r.mu.
func (r *Registry) syncLocked() {
	records := make([]ManagedRecord, 0, len(r.children))
	for _, child := range r.children {
		records = append(records, child.record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Pid < records[j].Pid })
	_ = r.WriteRecords(records)
}

// EnsureStarted returns the live child for spec.CacheKey, spawning one if none
// is running. Calling it twice for the same key spawns exactly one process.
func (r *Registry) EnsureStarted(ctx context.Context, spec SpawnSpec) (ManagedRecord, error) {
	key := strings.TrimSpace(spec.CacheKey)
	if key == "" {
		return ManagedRecord{}, errors.New("llamacpp: EnsureStarted requires a cacheKey")
	}
	binary, err := resolveExecutable(spec.BinaryPath)
	if err != nil {
		return ManagedRecord{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.children[key]; ok && isPidAlive(existing.record.Pid) {
		return existing.record, nil
	}

	cmd := exec.CommandContext(ctx, binary, spec.Args...)
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	setProcessGroup(cmd)
	tail := newTailBuffer(stderrTailLimit)
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return ManagedRecord{}, fmt.Errorf("start llama-server: %w", err)
	}

	child := &managedChild{
		stderr: tail,
		cmd:    cmd,
		record: ManagedRecord{
			Pid:       cmd.Process.Pid,
			Port:      spec.Port,
			ModelsDir: spec.ModelsDir,
			CacheKey:  key,
			StartedAt: time.Now().UTC(),
		},
		exited: make(chan struct{}),
	}
	r.children[key] = child
	r.syncLocked()

	go func() {
		err := cmd.Wait()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		} else if err != nil {
			code = -1
		}
		child.mu.Lock()
		child.exitCode = &code
		child.mu.Unlock()
		close(child.exited)
		r.mu.Lock()
		if cur, ok := r.children[key]; ok && cur == child {
			delete(r.children, key)
			r.syncLocked()
		}
		r.mu.Unlock()
	}()

	return child.record, nil
}

// child returns the live managed child for cacheKey, if any.
func (r *Registry) child(cacheKey string) *managedChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.children[strings.TrimSpace(cacheKey)]
}

// StderrTail exposes the bounded stderr tail of the child for cacheKey.
func (r *Registry) StderrTail(cacheKey string) string {
	if c := r.child(cacheKey); c != nil {
		return c.StderrTail()
	}
	return ""
}

// Stop terminates and deregisters the child for cacheKey. Unknown keys are a no-op.
func (r *Registry) Stop(ctx context.Context, cacheKey string) error {
	r.mu.Lock()
	child, ok := r.children[strings.TrimSpace(cacheKey)]
	if ok {
		delete(r.children, strings.TrimSpace(cacheKey))
	}
	r.syncLocked()
	r.mu.Unlock()
	if !ok {
		return nil
	}
	r.terminateChild(ctx, child)
	return nil
}

// ShutdownAll terminates every managed child and empties processes.json. It is
// a no-op (that still leaves an empty file) when nothing is running.
func (r *Registry) ShutdownAll(ctx context.Context) error {
	r.mu.Lock()
	children := make([]*managedChild, 0, len(r.children))
	for key, child := range r.children {
		children = append(children, child)
		delete(r.children, key)
	}
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, child := range children {
		wg.Add(1)
		go func(c *managedChild) {
			defer wg.Done()
			r.terminateChild(ctx, c)
		}(child)
	}
	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.syncLocked()
	return nil
}

// terminateChild sends SIGTERM, then SIGKILL once the grace period elapses.
func (r *Registry) terminateChild(ctx context.Context, child *managedChild) {
	if child == nil || child.cmd == nil || child.cmd.Process == nil {
		return
	}
	signalProcess(child.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-child.exited:
		return
	case <-ctx.Done():
	case <-time.After(r.killGrace):
	}
	signalProcess(child.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-child.exited:
	case <-time.After(500 * time.Millisecond):
	}
}

// ReapOrphansOnStartup kills llama-server processes recorded by a PRIOR run and
// truncates processes.json. It is idempotent per Registry instance; pids that
// are dead, unknown, or owned by this Registry are skipped without error.
func (r *Registry) ReapOrphansOnStartup() int {
	r.mu.Lock()
	if r.reapedOnce {
		r.mu.Unlock()
		return 0
	}
	r.reapedOnce = true
	owned := map[int]struct{}{}
	for _, child := range r.children {
		owned[child.record.Pid] = struct{}{}
	}
	r.mu.Unlock()

	reaped := 0
	for _, record := range r.ReadRecords() {
		if record.Pid <= 0 || record.Pid == os.Getpid() {
			continue
		}
		if _, mine := owned[record.Pid]; mine {
			continue
		}
		if !isPidAlive(record.Pid) {
			continue
		}
		if killPidGracefully(record.Pid, r.killGrace) {
			reaped++
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.syncLocked()
	return reaped
}

// killPidGracefully SIGTERMs pid and escalates to SIGKILL after timeout.
// Returns true when the pid is gone afterwards.
func killPidGracefully(pid int, timeout time.Duration) bool {
	if !isPidAlive(pid) {
		return false
	}
	signalProcess(pid, syscall.SIGTERM)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isPidAlive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	signalProcess(pid, syscall.SIGKILL)
	for i := 0; i < 20; i++ {
		if !isPidAlive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return !isPidAlive(pid)
}

// resolveExecutable validates a caller-supplied binary path before it reaches
// exec: paths arriving over RPC are never handed to the OS unchecked.
func resolveExecutable(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("llamacpp: binary path is required")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve binary path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("llama-server binary unavailable: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("llama-server binary is a directory: %s", abs)
	}
	return abs, nil
}

// OrphanCandidate is one non-managed llama-server process seen on this host.
type OrphanCandidate struct {
	Pid        int
	Executable string
}

// SelectWrongKindOrphans returns the llama-server processes whose executable
// does not match the selected binary kind. Processes this Registry owns — and
// llmd itself — are never selected. Ported from the TS
// terminateOrphanLlamaServersOfWrongKind, with the pgrep/ps shell-out replaced
// by listLlamaServerProcesses (which reports zero candidates, never an error,
// on platforms where scanning is unavailable).
func (r *Registry) SelectWrongKindOrphans(candidates []OrphanCandidate, mode string, selfManagedPath string) []OrphanCandidate {
	r.mu.Lock()
	owned := map[int]struct{}{os.Getpid(): {}}
	for _, child := range r.children {
		owned[child.record.Pid] = struct{}{}
	}
	r.mu.Unlock()

	selected := make([]OrphanCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Pid <= 0 {
			continue
		}
		if _, mine := owned[candidate.Pid]; mine {
			continue
		}
		if !isBinaryWrongKindForMode(candidate.Executable, mode, selfManagedPath) {
			continue
		}
		selected = append(selected, candidate)
	}
	return selected
}

// TerminateWrongKindOrphans scans the host for llama-server processes whose
// binary kind does not match mode and terminates them. Returns how many died.
func (r *Registry) TerminateWrongKindOrphans(mode string, selfManagedPath string) int {
	terminated := 0
	for _, candidate := range r.SelectWrongKindOrphans(listLlamaServerProcesses(), mode, selfManagedPath) {
		if killPidGracefully(candidate.Pid, r.killGrace) {
			terminated++
		}
	}
	return terminated
}

// isBinaryWrongKindForMode reports whether a running process's executable is
// inconsistent with the configured binary mode:
// in "builtin" mode anything outside the managed cache dir is wrong; in "self"
// mode anything that is not the configured self-managed binary is wrong.
func isBinaryWrongKindForMode(executable, mode, selfManagedPath string) bool {
	exe := strings.TrimSpace(executable)
	if exe == "" {
		return false
	}
	switch strings.TrimSpace(mode) {
	case "self":
		want := strings.TrimSpace(selfManagedPath)
		if want == "" {
			return false
		}
		return !samePath(exe, want)
	case "builtin":
		want := strings.TrimSpace(selfManagedPath)
		if want == "" {
			return false
		}
		return samePath(exe, want)
	default:
		return false
	}
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}
