package toolexec

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureStat is the metadata diskFileSource reports. It is a TEST type: llmd's
// tool seam carries no file capability, so there is no seam type to mirror here.
// A handler that needs file access closes over a source like this one.
type fixtureStat struct {
	Path  string
	Size  int64
	IsDir bool
}

// diskFileSource is workspace-relative read access backed by a plain on-disk
// directory (a t.TempDir() fixture workspace). Modelled on oracleDiskClient in
// internal/index/linked_oracle_fixture_test.go: no daemon, no gRPC, no network.
type diskFileSource struct{ root string }

// cleanRel is the fixture's own containment check. llmd has no workspace-path
// concept — dispatch never inspects a path — so this is deliberately local to
// the test fixture rather than borrowed from the seam.
func cleanRel(relPath string) (string, error) {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("path contains a null byte")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("path must be workspace-relative")
	}
	cleaned := path.Clean(filepath.ToSlash(trimmed))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path escapes the workspace")
	}
	return cleaned, nil
}

func (d diskFileSource) resolve(relPath string) (string, error) {
	cleaned, err := cleanRel(relPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(d.root, filepath.FromSlash(cleaned)), nil
}

func (d diskFileSource) ReadFile(_ context.Context, relPath string) ([]byte, error) {
	abs, err := d.resolve(relPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// WriteFile gives the fixture a write path guarded by the same containment
// check the read path uses.
func (d diskFileSource) WriteFile(_ context.Context, relPath string, data []byte) error {
	abs, err := d.resolve(relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

func (d diskFileSource) Stat(_ context.Context, relPath string) (fixtureStat, error) {
	abs, err := d.resolve(relPath)
	if err != nil {
		return fixtureStat{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fixtureStat{}, err
	}
	cleaned, _ := cleanRel(relPath)
	return fixtureStat{Path: cleaned, Size: fi.Size(), IsDir: fi.IsDir()}, nil
}

// newFixtureWorkspace writes files (path → contents) into a temp dir and
// returns a read/write source over it for handlers to close over.
func newFixtureWorkspace(t *testing.T, files map[string]string) diskFileSource {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return diskFileSource{root: root}
}

func TestFixtureWorkspace_readAndStat(t *testing.T) {
	fs := newFixtureWorkspace(t, map[string]string{"pkg/a.go": "package a\n"})
	b, err := fs.ReadFile(context.Background(), "pkg/a.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "package a\n" {
		t.Fatalf("content=%q", b)
	}
	st, err := fs.Stat(context.Background(), "pkg/a.go")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Path != "pkg/a.go" || st.Size != int64(len("package a\n")) || st.IsDir {
		t.Fatalf("stat=%+v", st)
	}
}

func TestFixtureWorkspace_rejectsEscapingPaths(t *testing.T) {
	fs := newFixtureWorkspace(t, map[string]string{"in.txt": "inside\n"})
	// A real file outside the fixture root, reachable only via "..".
	outside := filepath.Join(filepath.Dir(fs.root), "escape.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	for _, bad := range []string{"../escape.txt", "pkg/../../escape.txt", "/etc/passwd", "", "   "} {
		b, err := fs.ReadFile(context.Background(), bad)
		if err == nil {
			t.Fatalf("path %q should be refused", bad)
		}
		if len(b) != 0 {
			t.Fatalf("path %q returned %d bytes", bad, len(b))
		}
		if _, err := fs.Stat(context.Background(), bad); err == nil {
			t.Fatalf("stat %q should be refused", bad)
		}
	}
}

func TestFixtureWorkspace_cleanRelErrors(t *testing.T) {
	if _, err := cleanRel("a/./b.go"); err != nil {
		t.Fatalf("relative path should be accepted: %v", err)
	}
	if _, err := cleanRel("../x"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err=%v", err)
	}
}
