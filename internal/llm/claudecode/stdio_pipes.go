package claudecodeclillm

import (
	"fmt"
	"os"
	"os/exec"
)

// claudeCLIStdio owns the stdout/stderr pipes for a CLI spawn.
//
// We deliberately do NOT use cmd.StdoutPipe/StderrPipe: exec.Cmd.Wait closes
// those read ends as soon as the process exits, and we run Wait in a goroutine
// concurrently with the stream reader (procDone). That raced the reader and
// surfaced as "read |0: file already closed", dropping whatever was still
// buffered in the pipe — usually the final assistant text and the result line.
//
// With os.Pipe the read ends belong to us: Wait never touches them, the reader
// sees a clean EOF once the child (and anything it forked) closes its write
// ends, and every byte the CLI wrote is delivered.
type claudeCLIStdio struct {
	stdout         *os.File
	stderr         *os.File
	stdoutW        *os.File
	stderrW        *os.File
	stderrSnapshot func() string
}

// attachClaudeCLIStdio wires fresh pipes into cmd.Stdout/cmd.Stderr and starts
// the stderr logger. Call closeChildEnds right after cmd.Start (success or
// failure) and Close once the process is gone.
func attachClaudeCLIStdio(log cliLog, cmd *exec.Cmd) (*claudeCLIStdio, error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("claude-code-cli: stdout pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return nil, fmt.Errorf("claude-code-cli: stderr pipe: %w", err)
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	return &claudeCLIStdio{
		stdout:         outR,
		stderr:         errR,
		stdoutW:        outW,
		stderrW:        errW,
		stderrSnapshot: startClaudeCLIStderrLogger(log, errR),
	}, nil
}

// closeChildEnds closes the parent's copies of the write ends. Required after
// Start: otherwise our own open write fd keeps the readers from ever seeing EOF.
func (s *claudeCLIStdio) closeChildEnds() {
	if s.stdoutW != nil {
		_ = s.stdoutW.Close()
		s.stdoutW = nil
	}
	if s.stderrW != nil {
		_ = s.stderrW.Close()
		s.stderrW = nil
	}
}

// Close releases every fd. Closing the read ends also unblocks the reader
// goroutines if a stray grandchild still holds a write end open.
func (s *claudeCLIStdio) Close() {
	s.closeChildEnds()
	_ = s.stdout.Close()
	_ = s.stderr.Close()
}
