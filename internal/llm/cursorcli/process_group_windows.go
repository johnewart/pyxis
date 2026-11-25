//go:build windows

package cursorclillm

import "os/exec"

func setCursorCLIProcessGroup(cmd *exec.Cmd) {
	// Windows: no portable PGID; rely on terminating the root process.
	_ = cmd
}

func killCursorCLIProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
