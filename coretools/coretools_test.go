package coretools

import "testing"

// Every core tool but ask_user is guaranteed; ask_user is core (llmd's own,
// on by default) but a host may switch it off. A name that is not core is
// not guaranteed either.
func TestIsGuaranteed(t *testing.T) {
	for _, n := range Names() {
		if want := n != AskUser; IsGuaranteed(n) != want {
			t.Errorf("IsGuaranteed(%q) = %v, want %v", n, !want, want)
		}
		if !IsCore(n) {
			t.Errorf("IsCore(%q) = false", n)
		}
	}
	if IsGuaranteed("read_code") {
		t.Error("a product tool is guaranteed")
	}
}
