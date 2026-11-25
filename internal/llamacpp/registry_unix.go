//go:build !windows

/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// setProcessGroup puts the child in its own process group so we can signal the
// whole tree (house precedent: llm/claudecode/process_group_unix.go).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalProcess signals the child's process group, falling back to the pid.
func signalProcess(pid int, sig syscall.Signal) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, sig); err == nil {
		return
	}
	_ = syscall.Kill(pid, sig)
}

// isPidAlive reports whether pid names a live process.
func isPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// listLlamaServerProcesses enumerates llama-server processes on this host.
// Scanning failures yield zero candidates, never an error.
func listLlamaServerProcesses() []OrphanCandidate {
	if candidates, ok := scanProcFS(); ok {
		return candidates
	}
	return scanPS()
}

func scanProcFS() ([]OrphanCandidate, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	candidates := []OrphanCandidate{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		exe := string(bytes.SplitN(raw, []byte{0}, 2)[0])
		if !strings.Contains(exe, "llama-server") {
			continue
		}
		candidates = append(candidates, OrphanCandidate{Pid: pid, Executable: exe})
	}
	return candidates, true
}

func scanPS() []OrphanCandidate {
	out, err := exec.Command("ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil
	}
	candidates := []OrphanCandidate{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 2)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		exe := extractExecutable(fields[1])
		if !strings.Contains(exe, "llama-server") {
			continue
		}
		candidates = append(candidates, OrphanCandidate{Pid: pid, Executable: exe})
	}
	return candidates
}

// extractExecutable pulls argv[0] out of a ps args string, honouring quoting.
func extractExecutable(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return ""
	}
	for _, quote := range []byte{'"', '\''} {
		if trimmed[0] == quote {
			if end := strings.IndexByte(trimmed[1:], quote); end >= 0 {
				return trimmed[1 : end+1]
			}
		}
	}
	if space := strings.IndexByte(trimmed, ' '); space >= 0 {
		return trimmed[:space]
	}
	return trimmed
}
