//go:build windows

package claudecodeclillm

import "os/exec"

func setClaudeCLIProcessGroup(cmd *exec.Cmd) {}

func killClaudeCLIProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
