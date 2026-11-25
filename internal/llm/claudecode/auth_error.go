package claudecodeclillm

import (
	"errors"
	"fmt"
	"strings"
)

// ErrClaudeAuthExpired marks a CLI failure caused by missing or expired Claude
// credentials rather than by a bad request, a stale binary, or a broken stream.
//
// The Claude Code CLI has no headless re-auth: `claude auth login` is an
// interactive browser/SSO flow. So llmd cannot recover from this itself -- the
// only useful thing it can do is name the cause precisely enough that the caller
// can confirm with `claude auth status` and drive the interactive login over
// gRPC. Every other classification funnels into a generic "run failed", which
// is what made an expired token look like an llmd bug.
var ErrClaudeAuthExpired = errors.New("claude-code-cli: authentication required")

// claudeAuthFailureMarkers are substrings the CLI (or the API error it surfaces)
// emits when credentials are absent, rejected, or expired.
//
// These are matched against lowercased CLI stderr and JSON error results. The
// list is deliberately narrow: a false positive sends the user through a login
// flow that will not fix their problem, which is worse than the generic error.
// In particular nothing here matches on "401" or a bare "auth" substring, both
// of which show up in unrelated tool and MCP output.
var claudeAuthFailureMarkers = []string{
	"oauth token has expired",
	"oauth token expired",
	"refresh token has expired",
	"please run /login",
	"please run `claude login`",
	"please run claude login",
	"run /login to authenticate",
	"invalid api key",
	"failed to authenticate",
	"oauth access token is invalid",
	"authentication_error",
	"authentication error",
	"not authenticated",
	"unauthorized",
	"you are not logged in",
	"session has expired",
	"credentials have expired",
}

// IsClaudeAuthFailureText reports whether text looks like a Claude credential
// failure. Matching is case-insensitive on the raw CLI text.
func IsClaudeAuthFailureText(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	for _, marker := range claudeAuthFailureMarkers {
		if strings.Contains(t, marker) {
			return true
		}
	}
	return false
}

// IsClaudeAuthExpired reports whether err was classified as a credential failure.
func IsClaudeAuthExpired(err error) bool {
	return errors.Is(err, ErrClaudeAuthExpired)
}

// annotateClaudeAuthError tags err with [ErrClaudeAuthExpired] when err (or the
// accompanying CLI text, typically a stderr snapshot) names a credential failure.
//
// The original error is preserved in the chain so existing messages and any
// errors.Is checks against them keep working; this only adds a sentinel that
// callers can branch on. A nil err stays nil, and an already-tagged err is
// returned untouched so repeated wrapping at nested call sites is harmless.
func annotateClaudeAuthError(err error, text ...string) error {
	if err == nil || IsClaudeAuthExpired(err) {
		return err
	}
	if !IsClaudeAuthFailureText(err.Error()) {
		matched := false
		for _, t := range text {
			if IsClaudeAuthFailureText(t) {
				matched = true
				break
			}
		}
		if !matched {
			return err
		}
	}
	return fmt.Errorf("%w: %w", ErrClaudeAuthExpired, err)
}

// ClaudeAuthExpiredUserFacingError is the message shown when credentials fail.
// It names the interactive command because no non-interactive refresh exists.
func ClaudeAuthExpiredUserFacingError() string {
	return "Claude Code is not authenticated (the sign-in may have expired). " +
		"Sign in again with `claude auth login` for this provider's config directory."
}
