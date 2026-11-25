// Package clidiscovery finds agent CLI binaries (Claude Code, Cursor Agent) on
// the machine where llmd itself is running.
//
// This exists because llmd is frequently not running where the user is looking.
// The provider forms are served to a browser, and llmd may be in a container, on
// a remote host, or under a launcher whose PATH is nothing like the user's login
// shell PATH. Every other binary lookup in the tree (claudecode.resolveProbeBinary,
// cursorcli.Client.resolvedBinary, server.resolveProviderAuthBinary) does the same
// three-step dance -- override, env var, exec.LookPath -- and when that last step
// misses, the user gets `exec: "claude": executable file not found in $PATH` and no
// way forward except typing an absolute path they cannot see from where they sit.
//
// Discovery widens that last step: instead of consulting PATH alone, it looks in
// the directories these CLIs actually install into, probes each hit for a version,
// and reports everything it found so a caller can either auto-pick (see Resolve)
// or show the list.
//
// Reported paths are absolute but deliberately NOT symlink-resolved: symlinks
// are followed only to collapse duplicates.
//
// Scope note: discovery never mutates anything and never becomes an implicit
// fallback inside the hot path. Callers resolve ONCE, at provider-create time, and
// persist the absolute path they chose -- see providers.ResolveCLIBinaryForConfig.
// Re-scanning on every spawn would make which binary you run depend on the state
// of the filesystem at that moment, which is precisely the non-determinism this
// package is meant to remove.
package clidiscovery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnewart/pyxis/llmdlog"
)

// logModule tags discovery log lines. Discovery runs when a user adds a CLI
// provider and decides which binary that provider will use forever after, so
// what it found and what it picked are logged at INFO, not hidden behind DEBUG.
const logModule = "cli-discovery"

// Kind selects which CLI to look for.
type Kind string

const (
	// KindClaude is the Claude Code CLI (`claude`).
	KindClaude Kind = "claude"
	// KindCursor is the Cursor Agent CLI (`cursor-agent`, historically `agent`).
	KindCursor Kind = "cursor"
)

// Source records why a candidate was considered, which is the difference between
// "you told us this path" and "we guessed" when reporting results back to a user.
type Source string

const (
	// SourceOverride is an explicit binary path supplied by the caller/config.
	SourceOverride Source = "override"
	// SourceEnv is the CLI's dedicated environment variable.
	SourceEnv Source = "env"
	// SourcePath is a hit on one of the entries of $PATH.
	SourcePath Source = "path"
	// SourceWellKnown is a hit in a directory the CLI is known to install into.
	SourceWellKnown Source = "well_known"
)

// Candidate is one binary discovery found. A Candidate with Usable==false is
// still reported: "found it but it is too old / would not run" is far more useful
// to a user than silently dropping it and claiming nothing was found.
type Candidate struct {
	// Path is the absolute path to the binary as it was reached (symlink, env
	// value, PATH hit, well-known root). It is deliberately NOT symlink-resolved:
	// Claude Code and Cursor flip a stable symlink at versioned install dirs and
	// the versioned target is deleted on update, so a resolved path goes stale.
	Path string
	// Version is the CLI's self-reported version, or "" if it could not be read.
	Version string
	// Source is why this path was considered.
	Source Source
	// Origin is the env var name or directory this came from, for display.
	Origin string
	// Usable reports whether the binary ran and, where a floor applies, met it.
	Usable bool
	// Error explains !Usable in a sentence fit to show a user.
	Error string
}

// spec is the per-CLI knowledge: what it is called, which env var overrides it,
// and where it installs.
type spec struct {
	// names are the executable basenames to look for, most-preferred first.
	names []string
	// envVar is the dedicated override environment variable.
	envVar string
	// versionArgs is the argv that makes the CLI print its version.
	versionArgs []string
	// roots returns the well-known install directories to search, absolute and
	// already home-expanded. Entries that do not exist are skipped by the caller.
	roots func(home string) []string
	// checkVersion optionally rejects a version below a supported floor. It
	// returns "" when the version is acceptable, else a user-facing reason.
	checkVersion func(version string) string
}

// systemRoots are the machine-wide bin directories searched for every CLI. It is
// a package var rather than a constant so tests can empty it: otherwise a test
// that fakes HOME and PATH would still pick up a real CLI installed system-wide
// on the developer's or CI machine, making results depend on the host.
var systemRoots = []string{
	"/usr/local/bin",
	"/opt/homebrew/bin",
	"/usr/bin",
}

func specFor(k Kind) (spec, error) {
	switch k {
	case KindClaude:
		return spec{
			names:       []string{"claude"},
			envVar:      "CLAUDE_CLI_PATH",
			versionArgs: []string{"-v"},
			roots: func(home string) []string {
				return append([]string{
					// Claude Code's own installer target.
					filepath.Join(home, ".claude", "local"),
					filepath.Join(home, ".local", "bin"),
					filepath.Join(home, "bin"),
					// Global npm/bun/pnpm prefixes, where `npm i -g` lands.
					filepath.Join(home, ".npm-global", "bin"),
					filepath.Join(home, ".bun", "bin"),
					filepath.Join(home, "Library", "pnpm"),
					filepath.Join(home, ".local", "share", "pnpm"),
				}, systemRoots...)
			},
			checkVersion: claudeVersionFloor,
		}, nil
	case KindCursor:
		return spec{
			// `cursor-agent` is the current installed name; `agent` is the
			// historical one and is still what this tree defaults to, so both
			// are searched, current first.
			names:       []string{"cursor-agent", "agent"},
			envVar:      "CURSOR_AGENT_PATH",
			versionArgs: []string{"--version"},
			roots: func(home string) []string {
				return append([]string{
					filepath.Join(home, ".cursor", "bin"),
					filepath.Join(home, ".local", "bin"),
					filepath.Join(home, "bin"),
				}, systemRoots...)
			},
		}, nil
	default:
		return spec{}, fmt.Errorf("unsupported CLI kind %q", k)
	}
}

// claudeVersionFloorFn is swapped in by the claudecode package so this package
// can enforce the CLI version floor without importing it (which would be a cycle:
// claudecode would want to call discovery too). Nil means "no floor enforced".
var claudeVersionFloorFn func(version string) string

// SetClaudeVersionFloor installs the Claude Code minimum-version check. It is
// called from the claudecode package's init so that discovery and the spawn path
// agree on what "too old" means instead of duplicating the constant.
func SetClaudeVersionFloor(fn func(version string) string) { claudeVersionFloorFn = fn }

func claudeVersionFloor(v string) string {
	if claudeVersionFloorFn == nil {
		return ""
	}
	return claudeVersionFloorFn(v)
}

// probeTimeout bounds a single `<cli> --version` call. Discovery may run this
// once per candidate, so it has to fail fast on a binary that hangs rather than
// stalling the whole scan.
const probeTimeout = 10 * time.Second

// Discover scans for every installation of the given CLI that this process can
// see, in preference order: override, then env var, then $PATH, then well-known
// install directories. Paths are reported as reached (absolute, not
// symlink-resolved) and de-duplicated by their symlink-resolved target, so a
// binary reachable three ways is reported once, attributed to the
// highest-priority way.
//
// override may be empty. A non-empty override that does not resolve is reported
// as a single unusable Candidate rather than being silently dropped -- if the
// user named a path, the answer they need is why that path did not work.
func Discover(ctx context.Context, kind Kind, override string) ([]Candidate, error) {
	sp, err := specFor(kind)
	if err != nil {
		return nil, err
	}

	scanStarted := time.Now()
	var out []Candidate
	// seen keys on the symlink-resolved path so the same binary reached via
	// PATH and via a well-known root collapses to one entry.
	seen := make(map[string]struct{})
	add := func(path string, src Source, origin string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"skipping %s candidate %q: cannot make absolute: %v", kind, path, err)
			return
		}
		key := dedupeKey(abs)
		if key == "" {
			return
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		c := probe(ctx, sp, abs, src, origin)
		if c.Usable {
			llmdlog.Stdout(logModule, llmdlog.LevelInfo,
				"found %s: path=%s version=%s source=%s origin=%s", kind, c.Path, c.Version, c.Source, c.Origin)
		} else {
			// An unusable find is the most valuable line in the whole scan: it is
			// the difference between "not installed" and "installed but broken".
			llmdlog.Stdout(logModule, llmdlog.LevelWarn,
				"found %s but UNUSABLE: path=%s version=%s source=%s reason=%s",
				kind, c.Path, c.Version, c.Source, c.Error)
		}
		out = append(out, c)
	}

	// An explicit override ends the search: the caller asked about one specific
	// path, so the answer is that path's story and nothing else. Continuing to
	// scan would mix in installs the caller did not ask about and leave them
	// unable to tell which entry was theirs.
	if o := strings.TrimSpace(override); o != "" {
		llmdlog.Stdout(logModule, llmdlog.LevelInfo,
			"checking configured %s binary %q (no scan: an explicit path is authoritative)", kind, o)
		p, err := resolveGiven(o)
		if err != nil {
			llmdlog.Stdout(logModule, llmdlog.LevelError,
				"configured %s binary %q could not be resolved: %v", kind, o, err)
			return []Candidate{{
				Path:   o,
				Source: SourceOverride,
				Error:  fmt.Sprintf("configured binary %q could not be resolved: %v", o, err),
			}}, nil
		}
		add(p, SourceOverride, o)
		return out, nil
	}

	if ev := strings.TrimSpace(os.Getenv(sp.envVar)); ev != "" {
		if p, err := resolveGiven(ev); err == nil {
			add(p, SourceEnv, sp.envVar)
		}
	}

	for _, name := range sp.names {
		if p, err := exec.LookPath(name); err == nil {
			add(p, SourcePath, "$PATH")
		}
	}

	home, _ := os.UserHomeDir()
	for _, dir := range sp.roots(home) {
		if strings.Contains(dir, string(filepath.Separator)+".") && home == "" {
			// No home directory resolved; home-relative roots are meaningless.
			continue
		}
		for _, name := range sp.names {
			p := filepath.Join(dir, name)
			if isExecutableFile(p) {
				add(p, SourceWellKnown, dir)
			}
		}
	}

	// Usable candidates first, preserving discovery order within each group, so
	// a caller taking out[0] gets something that actually runs when one exists.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Usable && !out[j].Usable })

	if len(out) == 0 {
		// Log every location searched. "Found nothing" on its own tells the user
		// nothing about whether llmd looked anywhere they'd consider reasonable.
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"no %s CLI found in %s; searched: %s",
			kind, time.Since(scanStarted).Round(time.Millisecond),
			strings.Join(SearchedLocations(kind), ", "))
	} else {
		llmdlog.Stdout(logModule, llmdlog.LevelInfo,
			"%s scan finished in %s: %d candidate(s)",
			kind, time.Since(scanStarted).Round(time.Millisecond), len(out))
	}
	return out, nil
}

// SearchedLocations reports the places Discover consults, for telling a user
// where llmd actually looked. An empty result list is only meaningful alongside
// this -- "nothing found" without "here is where I searched" is not actionable.
func SearchedLocations(kind Kind) []string {
	sp, err := specFor(kind)
	if err != nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	out := []string{"$PATH", "$" + sp.envVar}
	return append(out, sp.roots(home)...)
}

// Resolve picks the one binary to use. An explicit override always wins and is
// never second-guessed beyond checking it resolves -- the user naming a path is a
// deliberate act, and silently substituting a "better" one would be worse than
// failing. With no override, the first usable discovered candidate is returned.
//
// The error names what was searched, because "not found" without "here is where I
// looked" leaves the user with nothing to act on.
func Resolve(ctx context.Context, kind Kind, override string) (Candidate, error) {
	cands, err := Discover(ctx, kind, override)
	if err != nil {
		return Candidate{}, err
	}
	for _, c := range cands {
		if c.Usable {
			return c, nil
		}
	}
	// Nothing usable. Prefer reporting a specific failure over a generic one:
	// an override or env hit that failed says more than "searched N dirs".
	for _, c := range cands {
		if c.Source == SourceOverride || c.Source == SourceEnv {
			return Candidate{}, fmt.Errorf("%s", c.Error)
		}
	}
	sp, _ := specFor(kind)
	home, _ := os.UserHomeDir()
	if len(cands) > 0 {
		return Candidate{}, fmt.Errorf(
			"found %s but none were usable (%s); set the provider's binary path explicitly",
			strings.Join(sp.names, "/"), cands[0].Error)
	}
	return Candidate{}, fmt.Errorf(
		"could not find the %s CLI on this machine (llmd searched $PATH, $%s, and %s). "+
			"Install it, or set the provider's binary path to its absolute location",
		strings.Join(sp.names, "/"), sp.envVar, strings.Join(sp.roots(home), ", "))
}

// probe runs the candidate's version command to confirm it is really the CLI and
// not just a file with the right name, and to apply any version floor.
func probe(ctx context.Context, sp spec, path string, src Source, origin string) Candidate {
	c := Candidate{Path: path, Source: src, Origin: origin}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(pctx, path, sp.versionArgs...)
	// Discovery must not inherit a caller's stdin; a CLI that decides to prompt
	// would otherwise block until the probe timeout.
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		c.Error = fmt.Sprintf("%s did not run: %v", path, err)
		return c
	}
	c.Version = parseVersion(string(out))
	if sp.checkVersion != nil {
		if reason := sp.checkVersion(c.Version); reason != "" {
			c.Error = reason
			return c
		}
	}
	c.Usable = true
	return c
}

// resolveGiven turns a user-supplied binary reference into an absolute path,
// treating a bare name as a PATH lookup and anything else as a filesystem path.
// This mirrors the override handling already in claudecode and cursorcli.
func resolveGiven(b string) (string, error) {
	b = strings.TrimSpace(b)
	if filepath.Base(b) == b {
		p, err := exec.LookPath(b)
		if err != nil {
			return "", fmt.Errorf("look up %q on PATH: %w", b, err)
		}
		return p, nil
	}
	abs, err := filepath.Abs(b)
	if err != nil {
		return "", err
	}
	if !isExecutableFile(abs) {
		return "", fmt.Errorf("%s is not an executable file", abs)
	}
	return abs, nil
}

// dedupeKey canonicalises an absolute path through symlinks, ONLY so duplicate
// routes to one binary (npm shims, ~/.claude/local/claude -> versions/<n>/claude)
// collapse to one entry. It is never reported to callers. Returns "" if the path
// can neither be resolved nor is an executable file.
func dedupeKey(p string) string {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		// A path that will not resolve is not usable, but keep the original so
		// the caller can still report it if it was an explicit override.
		if isExecutableFile(p) {
			return p
		}
		return ""
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return real
	}
	return abs
}

func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	// On Windows the executable bit is meaningless; presence is the test.
	if os.PathSeparator == '\\' {
		return true
	}
	return st.Mode().Perm()&0o111 != 0
}

// parseVersion pulls the bare version number out of a CLI's --version output.
//
// These CLIs decorate the number: `claude -v` prints "2.1.261 (Claude Code)".
// Handing that whole string to a semver parser fails, and a failed parse is
// indistinguishable from "no floor to apply" -- which is how a version floor
// ends up silently never firing. Extracting the leading numeric token keeps the
// comparison honest.
func parseVersion(s string) string { return ParseVersion(s) }

// ParseVersion is parseVersion, exported so the claudecode package can normalise
// versions the same way instead of keeping a second, subtly different copy.
func ParseVersion(s string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
	if line == "" {
		return ""
	}
	for _, field := range strings.Fields(line) {
		f := strings.TrimPrefix(field, "v")
		if f == "" {
			continue
		}
		// The first field that starts with a digit is the version number.
		if f[0] >= '0' && f[0] <= '9' {
			return f
		}
	}
	// No numeric token: return the line so the caller can still show something.
	return line
}
