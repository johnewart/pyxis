package toolexec

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolsession"
	"github.com/johnewart/pyxis/toolregistry"
)

// Harness runs tool calls end to end in ONE process: a real toolregistry, a real
// toolsession.Service and a fixture file source, with no gRPC server, no HTTP
// webhook, no daemon and no host process. See HARNESS.md.
//
// Capabilities are NOT handed to handlers by the harness, because the seam does
// not hand capabilities to handlers in production either. A test registers a
// handler that CLOSES OVER whatever it needs — h.Files, a fixture graph, a
// recording scheduler — exactly as a real registrar does.
type Harness struct {
	Registry *toolregistry.Registry
	Session  *toolsession.Service
	Handlers *Handlers
	// Files is a fixture workspace for handlers to close over. It is a plain
	// test type, not a seam type: llmd's tool seam has no file capability.
	Files diskFileSource

	SessionID string
	AgentID   string

	t       *testing.T
	callSeq int
}

// NewHarness builds a harness over a fixture workspace (path → contents).
func NewHarness(t *testing.T, files map[string]string) *Harness {
	t.Helper()
	return &Harness{
		Registry:  toolregistry.New(),
		Session:   toolsession.New(nil, nil, nil),
		Handlers:  NewHandlers(),
		Files:     newFixtureWorkspace(t, files),
		SessionID: "harness-session",
		AgentID:   "harness-agent",
		t:         t,
	}
}

// RegisterTool registers an inline-builtin tool: its definition in the registry
// and its handler in the harness's handler set. Registration is additive — a
// tool never registered here is absent from Snapshot and fails to dispatch.
func (h *Harness) RegisterTool(name string, classification llmv1.ToolClassification, fn Handler) {
	h.t.Helper()
	def := &llmv1.ToolDefinition{
		Name:           name,
		Classification: classification,
		Execution: &llmv1.ToolDefinition_InlineBuiltin{
			InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
		},
	}
	if err := h.Registry.Register(def); err != nil {
		h.t.Fatalf("register %s in registry: %v", name, err)
	}
	if err := h.Handlers.Register(name, fn); err != nil {
		h.t.Fatalf("register %s handler: %v", name, err)
	}
}

// Dispatch executes one tool call by name against the harness, exactly as the
// server-side tool loop would: registry lookup → toolexec.RunWithHandlers.
func (h *Harness) Dispatch(ctx context.Context, toolName, argsJSON string) (content string, success bool, errMsg string) {
	h.t.Helper()
	var def *llmv1.ToolDefinition
	for _, d := range h.Registry.Snapshot() {
		if d.GetName() == toolName {
			def = d
			break
		}
	}
	if def == nil {
		msg := fmt.Sprintf("tool %q is not registered", toolName)
		return msg, false, msg
	}
	h.callSeq++
	inv := Invocation{
		ToolCallID:    fmt.Sprintf("call-%d", h.callSeq),
		Name:          toolName,
		ArgumentsJSON: argsJSON,
		AgentID:       h.AgentID,
		SessionID:     h.SessionID,
		ReadOnly:      true,
	}
	return RunWithHandlers(ctx, def, inv, h.Handlers)
}

func TestHarness_dispatchesToHandlerWithParsedArgs(t *testing.T) {
	h := NewHarness(t, map[string]string{"a.txt": "hello\n"})
	var seen struct {
		Path string `json:"path"`
	}
	var sawSession, sawCallID string
	h.RegisterTool("probe", llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		func(_ context.Context, args json.RawMessage, inv Invocation) (Result, error) {
			if err := json.Unmarshal(args, &seen); err != nil {
				return Result{}, err
			}
			sawSession = inv.SessionID
			sawCallID = inv.ToolCallID
			// Closed over at registration, not delivered by the seam.
			b, err := h.Files.ReadFile(context.Background(), seen.Path)
			if err != nil {
				return Result{}, err
			}
			return Result{Content: string(b)}, nil
		})

	content, ok, errMsg := h.Dispatch(context.Background(), "probe", `{"path":"a.txt"}`)
	if !ok || errMsg != "" {
		t.Fatalf("ok=%v errMsg=%q", ok, errMsg)
	}
	if seen.Path != "a.txt" || content != "hello\n" {
		t.Fatalf("args=%+v content=%q", seen, content)
	}
	if sawSession != "harness-session" || sawCallID != "call-1" {
		t.Fatalf("session=%q callID=%q", sawSession, sawCallID)
	}
	if h.Session == nil {
		t.Fatal("harness must construct a tool session service")
	}
}

func TestHarness_unregisteredToolIsUnknown(t *testing.T) {
	h := NewHarness(t, nil)
	if snap := h.Registry.Snapshot(); len(snap) != 0 {
		t.Fatalf("fresh registry should be empty, got %d", len(snap))
	}
	_, ok, errMsg := h.Dispatch(context.Background(), "never_registered", `{}`)
	if ok || errMsg == "" {
		t.Fatalf("ok=%v errMsg=%q", ok, errMsg)
	}
}

func TestHarness_twoToolsInOneSession(t *testing.T) {
	h := NewHarness(t, map[string]string{"x.txt": "xs\n"})
	h.RegisterTool("tool_one", llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		func(context.Context, json.RawMessage, Invocation) (Result, error) {
			return Result{Content: "one"}, nil
		})
	h.RegisterTool("tool_two", llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
		func(context.Context, json.RawMessage, Invocation) (Result, error) {
			return Result{Content: "two"}, nil
		})
	if names := h.Handlers.Names(); len(names) != 2 {
		t.Fatalf("names=%v", names)
	}
	if c, ok, _ := h.Dispatch(context.Background(), "tool_one", `{}`); !ok || c != "one" {
		t.Fatalf("tool_one: ok=%v c=%q", ok, c)
	}
	if c, ok, _ := h.Dispatch(context.Background(), "tool_two", `{}`); !ok || c != "two" {
		t.Fatalf("tool_two: ok=%v c=%q", ok, c)
	}
}
