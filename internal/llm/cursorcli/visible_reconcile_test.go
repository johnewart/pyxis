package cursorclillm

import "testing"

// feedReconcile simulates the cursorcli visible loop: each step is either a type:text delta
// (relative, prefixed with "T:") or a type:assistant snapshot (absolute, prefixed with "A:").
// It returns the concatenation of every suffix fed to the pipeline — i.e. the visible text the
// user would see. A correct reconciliation yields the final message exactly once.
func feedReconcile(steps []string) (fed string) {
	var textRaw, asstRaw, fedRaw string
	for _, s := range steps {
		kind, payload := s[:2], s[2:]
		switch kind {
		case "T:":
			textRaw += payload
		case "A:":
			asstRaw = payload
		}
		suffix, newFed := reconcileVisibleSuffix(fedRaw, canonicalVisible(textRaw, asstRaw))
		fedRaw = newFed
		fed += suffix
	}
	return fed
}

func TestReconcile_pureIncrementalText(t *testing.T) {
	got := feedReconcile([]string{"T:Hello", "T: world", "T:!"})
	if got != "Hello world!" {
		t.Fatalf("got %q", got)
	}
}

func TestReconcile_pureAssistantSnapshots(t *testing.T) {
	got := feedReconcile([]string{"A:Hello", "A:Hello world", "A:Hello world!"})
	if got != "Hello world!" {
		t.Fatalf("got %q", got)
	}
}

// The exact long-lived bug: type:text streams the message (no trailing period), then a
// type:assistant snapshot repeats the final clause with an added period. Must not duplicate.
func TestReconcile_snapshotAddsTrailingPeriod_noDuplicate(t *testing.T) {
	s1 := "I'll find where backtesting is implemented and what metrics already exist, then add the performance metrics there. "
	s2 := "Core already tracks runtime via `BacktestRuntimeReport`; next I'll see what trading performance metrics exist and what's missing"
	got := feedReconcile([]string{
		"T:" + s1,
		"T:" + s2,
		"A:" + s1 + s2 + ".",
	})
	want := s1 + s2 + "."
	if got != want {
		t.Fatalf("duplicate:\n got=%q\nwant=%q", got, want)
	}
}

// Snapshot jumps ahead, then the late type:text increment it already absorbed arrives.
func TestReconcile_lateAbsorbedTextIncrement_noDuplicate(t *testing.T) {
	got := feedReconcile([]string{
		"T:S1",
		"A:S1 S2.",
		"T: S2",
	})
	if got != "S1 S2." {
		t.Fatalf("got %q want %q", got, "S1 S2.")
	}
}

func TestReconcile_interleavedClauses(t *testing.T) {
	// type:text deltas are incremental; type:assistant snapshots are cumulative. They
	// describe the same message and arrive interleaved.
	got := feedReconcile([]string{
		"T:Alpha",
		"A:Alpha beta",             // snapshot races ahead of the text stream
		"T: beta",                  // text increment already absorbed by the snapshot
		"T: gamma",                 // genuinely new text increment
		"A:Alpha beta gamma delta", // snapshot races ahead again
		"T: delta",                 // text increment already absorbed
	})
	if got != "Alpha beta gamma delta" {
		t.Fatalf("got %q", got)
	}
}

func TestCanonicalVisible(t *testing.T) {
	cases := []struct{ text, asst, want string }{
		{"", "x", "x"},
		{"x", "", "x"},
		{"ab", "abc", "abc"},   // asst extends text
		{"abc", "ab", "abc"},   // text extends asst
		{"abcd", "abx", "abcd"}, // divergence: prefer longer (text)
		{"abx", "abcd", "abcd"}, // divergence: prefer longer (asst)
	}
	for _, c := range cases {
		if got := canonicalVisible(c.text, c.asst); got != c.want {
			t.Fatalf("canonicalVisible(%q,%q)=%q want %q", c.text, c.asst, got, c.want)
		}
	}
}

func TestMaxOverlapLen(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"S1 S2", " S2.", 3},
		{"Hello", " world", 0},
		{"abcabc", "abcx", 3},
		{"", "x", 0},
	}
	for _, c := range cases {
		if got := maxOverlapLen(c.a, c.b); got != c.want {
			t.Fatalf("maxOverlapLen(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestReconcileVisibleSuffix_monotonicNoRewind(t *testing.T) {
	suffix, newFed := reconcileVisibleSuffix("Hello world", "Hello")
	if suffix != "" || newFed != "Hello world" {
		t.Fatalf("rewind: suffix=%q newFed=%q", suffix, newFed)
	}
}
