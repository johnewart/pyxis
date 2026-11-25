package claudecodeclillm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeProjectKey_matchesCLI(t *testing.T) {
	// Observed in ~/.claude/projects on a real install.
	cases := map[string]string{
		"/home/user/Code/project":              "-home-user-Code-project",
		"/opt/App/resources/app.asar.unpacked": "-opt-App-resources-app-asar-unpacked",
		"/tmp/llmd-session-6f1c-2a9e":          "-tmp-llmd-session-6f1c-2a9e",
	}
	for in, want := range cases {
		if got := claudeProjectKey(in); got != want {
			t.Errorf("claudeProjectKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPurgeSessionTranscripts_removesOnlyThatSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", t.TempDir())
	custom := t.TempDir()
	recordClaudeConfigDir(custom)

	keyFor := func(id string) string {
		return claudeProjectKey(filepath.Join(os.TempDir(), "llmd-session-"+id))
	}
	mk := func(root, key string) string {
		d := filepath.Join(root, "projects", key)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "x.jsonl"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		return d
	}
	defRoot := filepath.Join(home, ".claude")
	targetDefault := mk(defRoot, keyFor("sess-a"))
	targetCustom := mk(custom, keyFor("sess-a"))
	other := mk(defRoot, keyFor("sess-b"))
	project := mk(defRoot, "-home-me-Code-project")

	if err := PurgeSessionTranscripts("sess-a"); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{targetDefault, targetCustom} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("expected %s removed: %v", gone, err)
		}
	}
	for _, kept := range []string{other, project} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("expected %s kept: %v", kept, err)
		}
	}
	if err := PurgeSessionTranscripts(""); err != nil {
		t.Fatalf("empty id should be a no-op: %v", err)
	}
}
