//go:build !windows

package claudecodeclillm

import (
	"errors"
	"os/exec"
	"syscall"
)

func setClaudeCLIProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func killClaudeCLIProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		_ = cmd.Process.Kill()
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			_ = cmd.Process.Kill()
		}
	}
}
