// Package tooltimeout owns how long one tool call may run.
//
//   - LLMD_TOOL_DEFAULT_TIMEOUT (default 12m): the timeout a tool gets when it does
//     not declare its own (executor.max_duration_seconds).
//   - LLMD_TOOL_MAX_TIMEOUT (optional): an operator hard cap. When set, no tool
//     may run longer, whatever it declares, and the default is capped too. When
//     unset, tools get whatever they declare.
//
// The executor hub enforces ForTool(def) per call. Outer layers that wrap tool
// calls in their own single deadline (the Claude Code CLI's MCP_TOOL_TIMEOUT)
// use Backstop, which sits strictly above every offered tool's timeout so the
// hub's per-tool deadline always fires first and returns a structured error.
//
// Both variables accept a Go duration ("12m", "900s") or a bare integer in
// milliseconds. Invalid or non-positive values log a warning and are ignored.
package tooltimeout

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
)

const (
	// CapEnvVar names the optional operator hard cap.
	CapEnvVar = "LLMD_TOOL_MAX_TIMEOUT"
	// DefaultEnvVar overrides the timeout for tools that declare none.
	DefaultEnvVar = "LLMD_TOOL_DEFAULT_TIMEOUT"

	// BuiltinDefault leaves headroom above the 10-minute maximum a tool like
	// run_command may request.
	BuiltinDefault = 12 * time.Minute

	// Backstop margin: 10% of the slowest tool, kept within [MinMargin, MaxMargin].
	MinMargin = 10 * time.Second
	MaxMargin = 2 * time.Minute

	logModule = "tooltimeout"
)

// Cap returns the operator hard cap and whether one is set.
func Cap() (time.Duration, bool) {
	return parse(CapEnvVar, os.Getenv(CapEnvVar))
}

// Default returns the timeout for tools that declare none, capped by Cap.
func Default() time.Duration {
	d, ok := parse(DefaultEnvVar, os.Getenv(DefaultEnvVar))
	if !ok {
		d = BuiltinDefault
	}
	if c, capped := Cap(); capped && d > c {
		return c
	}
	return d
}

// ForTool returns the effective timeout for one tool: its declared
// executor.max_duration_seconds, else Default, limited by Cap when set.
// Tools without a declared value (non-executor kinds included) get Default.
func ForTool(def *llmv1.ToolDefinition) time.Duration {
	if s := def.GetExecutor().GetMaxDurationSeconds(); s > 0 {
		return clampDeclared(def.GetName(), time.Duration(s)*time.Second)
	}
	return Default()
}

// Backstop returns the single outer deadline for a turn offering defs: the
// slowest ForTool value plus Margin. With no tools it is Default plus Margin.
// It may exceed Cap: the cap bounds how long a tool runs, and the backstop must
// stay above that so it never fires before the hub's per-tool deadline.
func Backstop(defs []*llmv1.ToolDefinition) time.Duration {
	slowest := time.Duration(0)
	for _, d := range defs {
		if d == nil {
			continue
		}
		if t := ForTool(d); t > slowest {
			slowest = t
		}
	}
	if slowest == 0 {
		slowest = Default()
	}
	return slowest + Margin(slowest)
}

type backstopKey struct{}

// WithBackstop attaches a precomputed backstop to ctx. The server computes it
// from the FULL tool definitions (providers receive stripped definitions with
// no execution metadata, so they cannot compute it themselves).
func WithBackstop(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, backstopKey{}, d)
}

// BackstopFromContext returns the backstop attached by WithBackstop, if any.
func BackstopFromContext(ctx context.Context) (time.Duration, bool) {
	if ctx == nil {
		return 0, false
	}
	d, ok := ctx.Value(backstopKey{}).(time.Duration)
	return d, ok && d > 0
}

// Margin is 10% of d, kept within [MinMargin, MaxMargin].
func Margin(d time.Duration) time.Duration {
	m := d / 10
	if m < MinMargin {
		return MinMargin
	}
	if m > MaxMargin {
		return MaxMargin
	}
	return m
}

// warnedClamp records tools already warned about, so a capped tool logs once
// per process rather than on every call.
var warnedClamp sync.Map

func clampDeclared(name string, d time.Duration) time.Duration {
	c, ok := Cap()
	if !ok || d <= c {
		return d
	}
	if _, seen := warnedClamp.LoadOrStore(name+"|"+d.String()+"|"+c.String(), struct{}{}); !seen {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn,
			"tool %q declares max duration %s, above %s=%s; capping to %s", name, d, CapEnvVar, c, c)
	}
	return c
}

// parse reads a duration env value: Go duration or bare integer milliseconds.
// Empty → (0, false). Invalid/non-positive → warning, (0, false).
func parse(name, raw string) (time.Duration, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		d = time.Duration(ms) * time.Millisecond
	} else {
		pd, perr := time.ParseDuration(v)
		if perr != nil {
			llmdlog.Stdout(logModule, llmdlog.LevelWarn, "%s=%q is invalid (%v); ignoring", name, v, perr)
			return 0, false
		}
		d = pd
	}
	if d <= 0 {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "%s=%q is not positive; ignoring", name, v)
		return 0, false
	}
	return d, true
}
