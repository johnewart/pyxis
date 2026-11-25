package policyhook

import (
	"fmt"
	"strings"
)

// EnvFailMode is the environment variable that selects the fail mode.
const EnvFailMode = "LLMD_DEFAULT_POLICY_MODE"

// FailMode says what happens when a configured hook errors or times out.
type FailMode int

const (
	// FailClosed denies on hook failure (the default).
	FailClosed FailMode = iota
	// FailOpen allows on hook failure.
	FailOpen
)

func (m FailMode) String() string {
	if m == FailOpen {
		return "open"
	}
	return "closed"
}

// ParseFailMode parses "open" or "closed" (case-insensitive); empty is closed.
func ParseFailMode(s string) (FailMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "closed":
		return FailClosed, nil
	case "open":
		return FailOpen, nil
	}
	return FailClosed, fmt.Errorf("invalid %s %q: must be \"open\" or \"closed\"", EnvFailMode, s)
}

// FailModeFromEnv reads LLMD_DEFAULT_POLICY_MODE through getenv.
func FailModeFromEnv(getenv func(string) string) (FailMode, error) {
	return ParseFailMode(getenv(EnvFailMode))
}
