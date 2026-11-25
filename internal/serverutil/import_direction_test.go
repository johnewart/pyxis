package serverutil

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestImportDirection pins the one-way dependency direction: serverutil must
// stay reachable from graphserver/wsserver without importing back into
// internal/server (its former home) and re-creating a cycle-shaped coupling.
//
// serverutil imports only gRPC and the standard library. That is what lets
// internal/store and internal/knowledge/store import serverutil for Scope, so
// serverutil must never depend on either store package.
func TestImportDirection(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file location")
	}
	// this file lives at <llmdRoot>/internal/serverutil/import_direction_test.go
	llmdRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	cmd := exec.Command("go", "list", "-deps", "./internal/serverutil")
	cmd.Dir = llmdRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	forbidden := []string{
		"/internal/server",
		"/internal/store",
		"/internal/knowledge/store",
	}
	for _, line := range strings.Split(string(out), "\n") {
		for _, f := range forbidden {
			if strings.HasSuffix(line, f) {
				t.Fatalf("serverutil must not depend on %s, but go list -deps reported: %s", f, line)
			}
		}
	}
}
