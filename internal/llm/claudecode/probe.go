package claudecodeclillm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/johnewart/pyxis/internal/llm/clidiscovery"
	"github.com/johnewart/pyxis/llmdlog"
)

// probeLogModule tags every line emitted while validating a CLI provider, so the
// whole add-a-provider attempt can be followed with `grep claude-probe`.
const probeLogModule = "claude-probe"

// Register the CLI version floor with the discovery package, so a scan and a
// spawn agree on what "too old" means. Discovery cannot import this package
// directly (this package wants to use discovery), hence the callback.
func init() {
	clidiscovery.SetClaudeVersionFloor(func(version string) string {
		if claudeCLIVersionTooOld(version) {
			return claudeVersionTooOldError(version)
		}
		return ""
	})
}

// MinClaudeCLIVersion is the oldest Claude Code release llmd supports.
//
// Below this the CLI does not recognise the built-in tool name llmd passes via
// --tools (WaitForMcpServers). An unknown value there does not error -- the CLI
// resolves an EMPTY tool inventory and the llmd MCP server never finishes
// connecting, so every agent turn dies at the MCP inventory gate with a message
// that points at MCP rather than at the CLI version. Checking the floor here turns
// that into one clear sentence at provider-config time.
const MinClaudeCLIVersion = "2.1.224"

// claudeVersionCache memoises `claude -v` per binary path. The version is wanted on
// every spawn (it is logged and folded into gate errors) but cannot cost a
// subprocess each turn. Binaries are immutable per path in Claude's install layout,
// so caching on the path is safe for the life of the process.
var claudeVersionCache sync.Map // bin -> string

// CachedClaudeCLIVersion returns the CLI version for bin, running `claude -v` at
// most once per binary path. Returns "" when the version cannot be determined;
// callers must treat that as unknown rather than as a failure.
func CachedClaudeCLIVersion(ctx context.Context, bin, cwd string) string {
	b := strings.TrimSpace(bin)
	if b == "" {
		return ""
	}
	if v, ok := claudeVersionCache.Load(b); ok {
		s, _ := v.(string)
		return s
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ver := probeClaudeVersion(vctx, b, cwd)
	claudeVersionCache.Store(b, ver)
	return ver
}

// claudeCLIVersionTooOld reports whether version parses and is below
// MinClaudeCLIVersion. An unparseable or empty version yields false: refusing to run
// because we could not read a version string would trade a rare bug for a common one.
func claudeCLIVersionTooOld(version string) bool {
	v, err := semver.NewVersion(strings.TrimPrefix(strings.TrimSpace(version), "v"))
	if err != nil {
		return false
	}
	min, err := semver.NewVersion(MinClaudeCLIVersion)
	if err != nil {
		return false
	}
	return v.LessThan(min)
}

// claudeVersionTooOldError is the user-facing message for a below-floor CLI.
func claudeVersionTooOldError(version string) string {
	return fmt.Sprintf(
		"Claude Code %s is too old for llmd (minimum %s). Upgrade Claude Code, "+
			"and clear the provider's executable path if it points at a specific versions/<n> install dir.",
		strings.TrimSpace(version), MinClaudeCLIVersion)
}

// ProbeReason classifies WHY a probe failed.
//
// The flat Error string this used to carry is fine for a log line and useless
// to a UI: "not signed in" and "binary missing" need completely different
// affordances (a sign-in button vs install instructions), and a caller cannot
// reliably tell them apart by substring-matching a sentence meant for humans.
type ProbeReason string

const (
	ProbeReasonOK ProbeReason = "ok"
	// ProbeReasonBinaryNotFound: no CLI at the configured path / on this machine.
	ProbeReasonBinaryNotFound ProbeReason = "binary_not_found"
	// ProbeReasonVersionTooOld: found and runs, but below MinClaudeCLIVersion.
	ProbeReasonVersionTooOld ProbeReason = "version_too_old"
	// ProbeReasonNotAuthenticated: the CLI works but has no valid credentials.
	// This is the one the caller can actually fix, via interactive login.
	ProbeReasonNotAuthenticated ProbeReason = "not_authenticated"
	// ProbeReasonResumeUnsupported: this CLI cannot do headless --resume.
	ProbeReasonResumeUnsupported ProbeReason = "resume_unsupported"
	// ProbeReasonTimedOut: the CLI never exited within the probe timeout.
	ProbeReasonTimedOut ProbeReason = "timed_out"
	// ProbeReasonFailed: ran, failed, cause not classifiable.
	ProbeReasonFailed ProbeReason = "failed"
)

// ProbeOptions configures a capability/auth probe.
type ProbeOptions struct {
	Binary string
	Cwd    string
	// ConfigDir sets CLAUDE_CONFIG_DIR for the probe. It matters: Claude
	// credentials live per config directory, so probing without the provider's
	// ConfigDir checks a DIFFERENT account than the one an interactive login for
	// that provider would write to -- reporting "not signed in" to someone who
	// just signed in, or vice versa.
	ConfigDir string
	// OAuthToken is the provider's CLAUDE_CODE_OAUTH_TOKEN, if configured. The
	// probe must authenticate exactly as the provider will, so it gets the same
	// token (and the same stripping of any ambient one).
	OAuthToken string
	Timeout    time.Duration
}

// ResumeProbeResult is returned by [ProbeClaudeCodeResumeCapability].
type ResumeProbeResult struct {
	OK      bool
	Version string
	Error   string
	Details string
	// Reason classifies a failure for programmatic handling; ProbeReasonOK on success.
	Reason ProbeReason
	// Binary is the absolute path actually probed, when it could be resolved.
	Binary string
}

const claudeV2ProbeUserFacingError = "Claude CLI v2 requires Claude Code with headless session resume. Upgrade Claude Code or use Claude CLI (legacy)."

// claudeCheapCLIModel is the model for all non-user-facing CLI probes and integration tests.
// Without --model the CLI defaults to Opus, which is expensive for repeated provider scans.
const claudeCheapCLIModel = "haiku"

const claudeCheapCLISettings = `{"thinking":{"type":"disabled"}}`

// appendCheapClaudeCLIArgs appends shared cheap-model flags (haiku, one turn, low effort).
func appendCheapClaudeCLIArgs(args []string) []string {
	return append(args,
		"--model", claudeCheapCLIModel,
		"--max-turns", "1",
		"--effort", "low",
		"--settings", claudeCheapCLISettings,
	)
}

// resumeProbeChatArgs builds argv for headless JSON probe chats (always haiku, one turn).
func resumeProbeChatArgs(prompt string, extra ...string) []string {
	args := []string{"-p", prompt, "--output-format", "json"}
	args = appendCheapClaudeCLIArgs(args)
	return append(args, extra...)
}

// ClaudeV2ProbeUserFacingError is the message returned when resume capability checks fail.
func ClaudeV2ProbeUserFacingError() string {
	return claudeV2ProbeUserFacingError
}

// ProbeClaudeCodeResumeCapability checks that the Claude Code CLI supports headless --resume with JSON output.
//
// Logging here is INFO and always on, for the same reason argv logging is (see
// cliLog.exec): this runs when a user adds a provider, it shells out four times
// -- including two real (if cheap) model calls -- and can legitimately take a
// minute. Every failure mode it has collapses into one generic sentence about
// upgrading Claude Code, which is frequently the wrong advice. Without a stage
// trace, "adding a provider does nothing" is genuinely undiagnosable.
func ProbeClaudeCodeResumeCapability(binary, cwd string, timeout time.Duration) ResumeProbeResult {
	return ProbeClaudeCode(ProbeOptions{Binary: binary, Cwd: cwd, Timeout: timeout})
}

// ProbeClaudeCode is [ProbeClaudeCodeResumeCapability] with the full option set,
// including ConfigDir. Prefer this for auth checks: see ProbeOptions.ConfigDir.
func ProbeClaudeCode(opts ProbeOptions) ResumeProbeResult {
	binary, cwd := opts.Binary, opts.Cwd
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	started := time.Now()
	llmdlog.Stdout(probeLogModule, llmdlog.LevelInfo,
		"probe start: binary=%q cwd=%q config_dir=%q timeout=%s", binary, cwd, opts.ConfigDir, timeout)

	bin, err := resolveProbeBinary(binary)
	if err != nil {
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
			"probe failed at resolve-binary after %s: %v", time.Since(started).Round(time.Millisecond), err)
		return ResumeProbeResult{
			Reason:  ProbeReasonBinaryNotFound,
			Error:   "The Claude Code CLI could not be found. Install it, or set this provider's executable path.",
			Details: err.Error(),
		}
	}
	llmdlog.Stdout(probeLogModule, llmdlog.LevelInfo, "probe resolved binary: %s", bin)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// timedOut distinguishes "the CLI failed" from "we stopped waiting", which
	// otherwise both surface as an opaque `signal: killed`.
	timedOut := func() bool { return ctx.Err() != nil }

	version := probeClaudeVersion(ctx, bin, cwd)
	llmdlog.Stdout(probeLogModule, llmdlog.LevelInfo, "probe cli version: %q (minimum %s)",
		version, MinClaudeCLIVersion)
	if claudeCLIVersionTooOld(version) {
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
			"probe failed: cli version %q is below minimum %s", version, MinClaudeCLIVersion)
		return ResumeProbeResult{
			Version: version,
			Binary:  bin,
			Reason:  ProbeReasonVersionTooOld,
			Error:   claudeVersionTooOldError(version),
			Details: fmt.Sprintf("resolved binary: %s", bin),
		}
	}
	helpOut, helpErr := runProbeCmdIn(ctx, bin, cwd, opts.ConfigDir, opts.OAuthToken, "-h")
	if helpErr != nil {
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError, "probe failed at --help: %v", helpErr)
		return ResumeProbeResult{
			Version: version,
			Binary:  bin,
			Reason:  reasonForProbeErr(helpErr, timedOut()),
			Error:   claudeV2ProbeUserFacingError,
			Details: fmt.Sprintf("claude --help: %v", helpErr),
		}
	}
	if !strings.Contains(helpOut, "--resume") || !strings.Contains(helpOut, "--output-format") {
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
			"probe failed: claude --help lists neither --resume nor --output-format; this CLI cannot do headless resume")
		return ResumeProbeResult{
			Version: version,
			Binary:  bin,
			Reason:  ProbeReasonResumeUnsupported,
			Error:   claudeV2ProbeUserFacingError,
			Details: "claude --help missing --resume or --output-format",
		}
	}
	// The next two steps are real model calls. Say so before blocking on them:
	// this is where an "adding a provider hangs" report actually comes from.
	llmdlog.Stdout(probeLogModule, llmdlog.LevelInfo,
		"probe creating throwaway %s session (this makes a real model call)", claudeCheapCLIModel)

	createOut, createErr := runProbeCmdIn(ctx, bin, cwd, opts.ConfigDir, opts.OAuthToken, resumeProbeChatArgs("Reply with exactly: ok")...)
	if createErr != nil {
		// An unauthenticated CLI fails this probe chat like any other broken CLI.
		// Without this check the user is told to upgrade Claude Code when all they
		// need is to sign in again.
		if IsClaudeAuthFailureText(createErr.Error()) {
			llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
				"probe failed: claude CLI is not authenticated (run `claude login`): %v", createErr)
			return ResumeProbeResult{
				Version: version,
				Binary:  bin,
				Reason:  ProbeReasonNotAuthenticated,
				Error:   ClaudeAuthExpiredUserFacingError(),
				Details: fmt.Sprintf("create session: %v", createErr),
			}
		}
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError, "probe failed at create-session: %v", createErr)
		return ResumeProbeResult{
			Version: version,
			Binary:  bin,
			Reason:  reasonForProbeErr(createErr, timedOut()),
			Error:   claudeV2ProbeUserFacingError,
			Details: fmt.Sprintf("create session: %v", createErr),
		}
	}
	sessionID, parseErr := parseClaudeJSONSessionID(createOut)
	if parseErr != nil {
		if IsClaudeAuthFailureText(parseErr.Error()) {
			llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
				"probe failed: claude CLI is not authenticated (run `claude login`): %v", parseErr)
			return ResumeProbeResult{
				Version: version,
				Binary:  bin,
				Reason:  ProbeReasonNotAuthenticated,
				Error:   ClaudeAuthExpiredUserFacingError(),
				Details: parseErr.Error(),
			}
		}
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
			"probe failed parsing create-session JSON: %v", parseErr)
		return ResumeProbeResult{
			Version: version,
			Binary:  bin,
			Reason:  ProbeReasonFailed,
			Error:   claudeV2ProbeUserFacingError,
			Details: parseErr.Error(),
		}
	}
	llmdlog.Stdout(probeLogModule, llmdlog.LevelInfo,
		"probe created session %s; resuming it to confirm headless resume works", sessionID)

	resumeOut, resumeErr := runProbeCmdIn(ctx, bin, cwd, opts.ConfigDir, opts.OAuthToken, resumeProbeChatArgs("Reply with exactly: ok2", "--resume", sessionID)...)
	if resumeErr != nil {
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError, "probe failed at resume-session: %v", resumeErr)
		return ResumeProbeResult{
			Version: version,
			Binary:  bin,
			Reason:  reasonForProbeErr(resumeErr, timedOut()),
			Error:   claudeV2ProbeUserFacingError,
			Details: fmt.Sprintf("resume session: %v", resumeErr),
		}
	}
	if _, err := parseClaudeJSONSessionID(resumeOut); err != nil {
		llmdlog.Stdout(probeLogModule, llmdlog.LevelError, "probe failed parsing resume JSON: %v", err)
		return ResumeProbeResult{
			Version: version,
			Binary:  bin,
			Reason:  ProbeReasonResumeUnsupported,
			Error:   claudeV2ProbeUserFacingError,
			Details: fmt.Sprintf("resume parse: %v", err),
		}
	}
	llmdlog.Stdout(probeLogModule, llmdlog.LevelInfo,
		"probe OK in %s: binary=%s version=%s", time.Since(started).Round(time.Millisecond), bin, version)
	return ResumeProbeResult{OK: true, Version: version, Binary: bin, Reason: ProbeReasonOK}
}

// reasonForProbeErr classifies a subprocess failure. Auth is checked even on
// steps that are not the login-sensitive ones, because the CLI reports expired
// credentials from whichever command happens to run first.
func reasonForProbeErr(err error, timedOut bool) ProbeReason {
	if timedOut {
		return ProbeReasonTimedOut
	}
	if err != nil && IsClaudeAuthFailureText(err.Error()) {
		return ProbeReasonNotAuthenticated
	}
	return ProbeReasonFailed
}

func resolveProbeBinary(binary string) (string, error) {
	b := strings.TrimSpace(binary)
	if b == "" {
		b = strings.TrimSpace(os.Getenv("CLAUDE_CLI_PATH"))
	}
	if b == "" {
		b = "claude"
	}
	if filepath.Base(b) == b {
		path, err := exec.LookPath(b)
		if err != nil {
			return "", fmt.Errorf("look up %q: %w", b, err)
		}
		return path, nil
	}
	return filepath.Abs(b)
}

func probeClaudeVersion(ctx context.Context, bin, cwd string) string {
	out, err := runProbeCmd(ctx, bin, cwd, "-v")
	if err != nil {
		return ""
	}
	// Normalise before returning. `claude -v` prints "2.1.261 (Claude Code)",
	// which is not parseable as semver -- and since claudeCLIVersionTooOld treats
	// an unparseable version as "not too old", returning the raw line silently
	// disabled the MinClaudeCLIVersion floor entirely. It also leaks the suffix
	// into the version field reported over the API.
	line := clidiscovery.ParseVersion(out)
	if line == "" {
		return ""
	}
	if v, err := semver.NewVersion(strings.TrimPrefix(line, "v")); err == nil {
		return v.String()
	}
	return line
}

// runProbeCmd runs one probe subprocess, logging its argv, duration and outcome.
//
// The argv line is DEBUG (these are short, fixed, uninteresting when they work)
// but a FAILURE is always logged at ERROR with the CLI's own stderr attached.
// The CLI's stderr is the only place that says WHY -- everything above this
// function flattens it into one generic "upgrade Claude Code" sentence.
func runProbeCmd(ctx context.Context, bin, cwd string, args ...string) (string, error) {
	return runProbeCmdIn(ctx, bin, cwd, "", "", args...)
}

// runProbeCmdIn is runProbeCmd with an explicit CLAUDE_CONFIG_DIR. Credentials
// are stored per config directory, so any probe that touches authentication has
// to run against the SAME directory the provider (and its interactive login)
// uses, or it reports on an account the user never configured.
func runProbeCmdIn(ctx context.Context, bin, cwd, configDir, oauthToken string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	if cd := strings.TrimSpace(cwd); cd != "" {
		cmd.Dir = cd
	}
	// Same env policy as a real turn: ambient credentials and CLAUDE_CONFIG_DIR
	// stripped, the provider's own values set.
	cmd.Env = appendClaudeConfigDirEnv(claudeCLIEnv(oauthToken), configDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Presence and length only, never the value: "Not logged in" is
	// indistinguishable from "token never arrived" without this.
	tokenInEnv := 0
	for _, e := range cmd.Env {
		if v, ok := strings.CutPrefix(e, claudeOAuthTokenEnv+"="); ok {
			tokenInEnv = len(v)
		}
	}
	llmdlog.Stdout(probeLogModule, llmdlog.LevelDebug, "probe exec: %s argv=[%s] oauth_token_len=%d config_dir=%q",
		bin, strings.Join(redactClaudeCLIArgs(args), " "), tokenInEnv, strings.TrimSpace(configDir))
	started := time.Now()
	err := cmd.Run()
	elapsed := time.Since(started).Round(time.Millisecond)

	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		// In --output-format json mode the CLI reports failures (a rejected
		// token, "Not logged in") as a result object on STDOUT with an empty
		// stderr and a non-zero exit. Without this the real reason is dropped
		// and the probe falls through to the generic "upgrade Claude Code"
		// sentence, which is wrong advice for an auth failure.
		if msg == "" {
			var r claudeJSONResult
			if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &r) == nil && strings.TrimSpace(r.Result) != "" {
				msg = strings.TrimSpace(r.Result)
			}
		}
		if msg == "" {
			msg = err.Error()
		}
		// Name the timeout explicitly: a context deadline shows up as a bare
		// "signal: killed", which reads like a crash rather than "we waited".
		if ctx.Err() != nil {
			llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
				"probe exec TIMED OUT after %s: %s argv=[%s]; the CLI never exited",
				elapsed, bin, strings.Join(redactClaudeCLIArgs(args), " "))
		} else {
			llmdlog.Stdout(probeLogModule, llmdlog.LevelError,
				"probe exec failed after %s: %s argv=[%s] err=%v stderr=%s",
				elapsed, bin, strings.Join(redactClaudeCLIArgs(args), " "), err, truncateForLog(msg))
		}
		return stdout.String(), fmt.Errorf("%w: %s", err, msg)
	}
	llmdlog.Stdout(probeLogModule, llmdlog.LevelDebug, "probe exec ok in %s (%d bytes stdout)",
		elapsed, stdout.Len())
	return stdout.String(), nil
}

type claudeJSONResult struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error"`
}

// parseClaudeJSONSessionID extracts session_id from Claude Code --output-format json stdout.
func parseClaudeJSONSessionID(stdout string) (string, error) {
	lines := bytes.Split(bytes.TrimSpace([]byte(stdout)), []byte("\n"))
	var last *claudeJSONResult
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var r claudeJSONResult
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.Type == "result" || strings.TrimSpace(r.SessionID) != "" {
			last = &r
			if strings.TrimSpace(r.SessionID) != "" {
				break
			}
		}
	}
	if last == nil {
		return "", fmt.Errorf("no JSON result object in CLI output")
	}
	if last.IsError || (last.Subtype != "" && last.Subtype != "success") {
		return "", fmt.Errorf("CLI error result: %s", strings.TrimSpace(last.Result))
	}
	sid := strings.TrimSpace(last.SessionID)
	if sid == "" {
		return "", fmt.Errorf("JSON result missing session_id")
	}
	return sid, nil
}
