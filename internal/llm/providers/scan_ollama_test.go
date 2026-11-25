package providers

import "testing"

func TestOllamaNativeBase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "http://127.0.0.1:11434"},
		{"blank", "   ", "http://127.0.0.1:11434"},
		{"root", "http://127.0.0.1:11434", "http://127.0.0.1:11434"},
		{"trailing slash", "http://127.0.0.1:11434/", "http://127.0.0.1:11434"},
		{"url used verbatim", "http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1"},
		{"trailing slash only removed", "http://127.0.0.1:11434/v1/", "http://127.0.0.1:11434/v1"},
		{"remote host", "https://ollama.example.com", "https://ollama.example.com"},
		{"path prefix preserved", "http://host/ollama", "http://host/ollama"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ollamaNativeBase(tc.in); got != tc.want {
				t.Fatalf("ollamaNativeBase(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
