//go:build windows

/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package llamacpp

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup is a no-op on Windows; job objects are out of scope here.
func setProcessGroup(cmd *exec.Cmd) {}

// signalProcess kills the process; Windows has no SIGTERM semantics for us.
func signalProcess(pid int, sig syscall.Signal) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
}

// isPidAlive reports whether pid names a live process.
func isPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}

// listLlamaServerProcesses is unavailable on Windows: zero candidates, no error.
func listLlamaServerProcesses() []OrphanCandidate { return nil }
