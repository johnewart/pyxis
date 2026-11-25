/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// sleepBinary resolves a long-lived, hermetic fake "llama-server" child.
func sleepBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	return path
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	return NewRegistry(RegistryOptions{StateDir: t.TempDir(), TerminateTimeout: 500 * time.Millisecond})
}

func waitForPidGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isPidAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive", pid)
}

func TestRegistryRecordsRoundTripThroughFile(t *testing.T) {
	reg := newTestRegistry(t)
	started := time.Now().UTC().Truncate(time.Second)
	want := ManagedRecord{Pid: 4242, Port: 8123, ModelsDir: "/models", CacheKey: "key-a", StartedAt: started}

	if err := reg.WriteRecords([]ManagedRecord{want}); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(reg.FilePath())); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}

	got := reg.ReadRecords()
	if len(got) != 1 || got[0].Pid != want.Pid || got[0].Port != want.Port ||
		got[0].CacheKey != want.CacheKey || got[0].ModelsDir != want.ModelsDir ||
		!got[0].StartedAt.Equal(want.StartedAt) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestRegistryMissingOrCorruptFileYieldsNoRecords(t *testing.T) {
	reg := newTestRegistry(t)
	if got := reg.ReadRecords(); len(got) != 0 {
		t.Fatalf("expected no records for absent file, got %+v", got)
	}
	if err := os.MkdirAll(filepath.Dir(reg.FilePath()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(reg.FilePath(), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := reg.ReadRecords(); len(got) != 0 {
		t.Fatalf("expected no records for corrupt file, got %+v", got)
	}
}

func TestSpawnIsSingleProcessPerCacheKey(t *testing.T) {
	reg := newTestRegistry(t)
	spec := SpawnSpec{CacheKey: "model-a", BinaryPath: sleepBinary(t), Args: []string{"30"}, Port: 8080}

	first, err := reg.EnsureStarted(context.Background(), spec)
	if err != nil {
		t.Fatalf("first EnsureStarted: %v", err)
	}
	second, err := reg.EnsureStarted(context.Background(), spec)
	if err != nil {
		t.Fatalf("second EnsureStarted: %v", err)
	}
	if first.Pid != second.Pid {
		t.Fatalf("expected one process, got pids %d and %d", first.Pid, second.Pid)
	}
	if !isPidAlive(first.Pid) {
		t.Fatalf("child %d is not running", first.Pid)
	}

	records := reg.ReadRecords()
	if len(records) != 1 || records[0].Pid != first.Pid || records[0].CacheKey != "model-a" {
		t.Fatalf("expected one persisted record, got %+v", records)
	}

	t.Cleanup(func() { _ = reg.ShutdownAll(context.Background()) })
}

func TestSpawnRejectsMissingBinaryAndCacheKey(t *testing.T) {
	reg := newTestRegistry(t)
	if _, err := reg.EnsureStarted(context.Background(), SpawnSpec{BinaryPath: sleepBinary(t)}); err == nil {
		t.Fatal("expected error for empty cacheKey")
	}
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := reg.EnsureStarted(context.Background(), SpawnSpec{CacheKey: "k", BinaryPath: missing}); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestRegistryReapsOrphansFromPriorRun(t *testing.T) {
	orphan := exec.Command(sleepBinary(t), "30")
	if err := orphan.Start(); err != nil {
		t.Fatalf("start orphan: %v", err)
	}
	pid := orphan.Process.Pid
	go func() { _ = orphan.Wait() }()

	reg := newTestRegistry(t)
	if err := reg.WriteRecords([]ManagedRecord{{Pid: pid, CacheKey: "prior", StartedAt: time.Now().UTC()}}); err != nil {
		t.Fatalf("seed records: %v", err)
	}

	// A fresh Registry over the same dir is the "next run".
	next := NewRegistry(RegistryOptions{StateDir: reg.stateDir, TerminateTimeout: 500 * time.Millisecond})
	if reaped := next.ReapOrphansOnStartup(); reaped != 1 {
		t.Fatalf("expected 1 reaped, got %d", reaped)
	}
	waitForPidGone(t, pid)

	if got := next.ReadRecords(); len(got) != 0 {
		t.Fatalf("expected registry file truncated, got %+v", got)
	}
	if reaped := next.ReapOrphansOnStartup(); reaped != 0 {
		t.Fatalf("expected idempotent reap, got %d", reaped)
	}
}

func TestRegistryReapSkipsDeadAndUnknownPids(t *testing.T) {
	reg := newTestRegistry(t)
	dead := exec.Command(sleepBinary(t), "0")
	if err := dead.Run(); err != nil {
		t.Fatalf("run short-lived child: %v", err)
	}
	records := []ManagedRecord{
		{Pid: dead.Process.Pid, StartedAt: time.Now().UTC()},
		{Pid: 0, StartedAt: time.Now().UTC()},
		{Pid: os.Getpid(), StartedAt: time.Now().UTC()},
	}
	if err := reg.WriteRecords(records); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if reaped := reg.ReapOrphansOnStartup(); reaped != 0 {
		t.Fatalf("expected nothing reaped, got %d", reaped)
	}
	if got := reg.ReadRecords(); len(got) != 0 {
		t.Fatalf("expected truncated file, got %+v", got)
	}
}

func TestShutdownAllTerminatesAndDeregisters(t *testing.T) {
	reg := newTestRegistry(t)
	bin := sleepBinary(t)

	a, err := reg.EnsureStarted(context.Background(), SpawnSpec{CacheKey: "a", BinaryPath: bin, Args: []string{"30"}})
	if err != nil {
		t.Fatalf("spawn a: %v", err)
	}
	b, err := reg.EnsureStarted(context.Background(), SpawnSpec{CacheKey: "b", BinaryPath: bin, Args: []string{"30"}})
	if err != nil {
		t.Fatalf("spawn b: %v", err)
	}

	if err := reg.ShutdownAll(context.Background()); err != nil {
		t.Fatalf("ShutdownAll: %v", err)
	}
	waitForPidGone(t, a.Pid)
	waitForPidGone(t, b.Pid)

	if got := reg.ReadRecords(); len(got) != 0 {
		t.Fatalf("expected empty registry, got %+v", got)
	}
}

func TestShutdownAllOnEmptyRegistryWritesEmptyFile(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.ShutdownAll(context.Background()); err != nil {
		t.Fatalf("ShutdownAll: %v", err)
	}
	raw, err := os.ReadFile(reg.FilePath())
	if err != nil {
		t.Fatalf("registry file missing: %v", err)
	}
	var parsed processRegistryFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Processes) != 0 {
		t.Fatalf("expected zero records, got %+v", parsed.Processes)
	}
}

func TestShutdownStopTerminatesOneChild(t *testing.T) {
	reg := newTestRegistry(t)
	rec, err := reg.EnsureStarted(context.Background(), SpawnSpec{CacheKey: "solo", BinaryPath: sleepBinary(t), Args: []string{"30"}})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := reg.Stop(context.Background(), "solo"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForPidGone(t, rec.Pid)
	if got := reg.ReadRecords(); len(got) != 0 {
		t.Fatalf("expected deregistration, got %+v", got)
	}
	if err := reg.Stop(context.Background(), "unknown"); err != nil {
		t.Fatalf("Stop unknown: %v", err)
	}
}

func TestRegistryWrongKindSweepNeverSelectsOwnedPids(t *testing.T) {
	reg := newTestRegistry(t)
	bin := sleepBinary(t)
	own, err := reg.EnsureStarted(context.Background(), SpawnSpec{CacheKey: "owned", BinaryPath: bin, Args: []string{"30"}})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = reg.ShutdownAll(context.Background()) })

	candidates := []OrphanCandidate{
		{Pid: own.Pid, Executable: "/somewhere/else/llama-server"},
		{Pid: os.Getpid(), Executable: "/somewhere/else/llama-server"},
		{Pid: 999999, Executable: "/somewhere/else/llama-server"},
	}
	selected := reg.SelectWrongKindOrphans(candidates, "self", "/managed/llama-server")
	if len(selected) != 1 || selected[0].Pid != 999999 {
		t.Fatalf("expected only the foreign pid, got %+v", selected)
	}

	// The configured self-managed binary is the right kind and is left alone.
	matching := []OrphanCandidate{{Pid: 999999, Executable: "/managed/llama-server"}}
	if got := reg.SelectWrongKindOrphans(matching, "self", "/managed/llama-server"); len(got) != 0 {
		t.Fatalf("expected right-kind binary to be skipped, got %+v", got)
	}
}
