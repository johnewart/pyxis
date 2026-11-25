//go:build !windows

package claudecodeclillm

import (
	"io"
	"os/exec"
	"testing"
)

// Regression: cmd.Wait running concurrently with the stdout reader used to close
// the read end (cmd.StdoutPipe), losing buffered output with
// "read |0: file already closed". Owned os.Pipes must deliver every byte.
func TestClaudeCLIStdio_ConcurrentWaitDeliversAllOutput(t *testing.T) {
	// Below the pipe buffer (64 KiB on Linux/macOS) so the child can exit with
	// unread output still in the pipe — the exact window the old code lost.
	const size = 32 << 10
	for i := 0; i < 5; i++ {
		cmd := exec.Command("sh", "-c", "head -c 32768 /dev/zero; echo oops >&2")
		stdio, err := attachClaudeCLIStdio(newCLILog("test"), cmd)
		if err != nil {
			t.Fatalf("attach: %v", err)
		}
		err = cmd.Start()
		stdio.closeChildEnds()
		if err != nil {
			stdio.Close()
			t.Fatalf("start: %v", err)
		}
		waited := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waited)
		}()
		<-waited // process gone (and Wait done) before we've read anything

		got, err := io.ReadAll(stdio.stdout)
		stdio.Close()
		if err != nil {
			t.Fatalf("iteration %d: read: %v", i, err)
		}
		if len(got) != size {
			t.Fatalf("iteration %d: got %d bytes, want %d", i, len(got), size)
		}
	}
}
