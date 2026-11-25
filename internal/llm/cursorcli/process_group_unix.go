//go:build !windows

package cursorclillm

import (
	"errors"
	"os/exec"
	"syscall"
)

func setCursorCLIProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Put the CLI in its own process group so cancellation can signal the whole tree.
	cmd.SysProcAttr.Setpgid = true
}

func killCursorCLIProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		_ = cmd.Process.Kill()
		return
	}
	// Negative PID targets the process group when Setpgid created a new group for the child.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			_ = cmd.Process.Kill()
		}
	}
}
