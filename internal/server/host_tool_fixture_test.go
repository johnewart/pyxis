package server

// host_tool_fixture_test.go holds the fakes builtin_tools_test.go,
// invoke_tool_test.go and agent_loop_test.go share for exercising the
// inline-builtin dispatch path.
//
// llmd core defines no project-inspection tools of its own: a host registers
// them through InstallBuiltinTools' extra-registrar seam. What these tests own
// is llmd's own responsibility — that InstallBuiltinTools registers a name as an
// inline builtin with a live handler, and that the per-invocation
// Invocation.Scope reaching that handler is the one bound to the request.
// llmd resolves and hands over nothing else.
//
// The fake tools are deliberately generic (host_fetch / host_related /
// host_find). They model the SHAPE a host registrar uses — a handler closing
// over its own per-workspace data and resolving it from inv.Scope.TenantID — not
// any particular host's tool. A real tool's behaviour is that host's to test.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolregistry"
)

// registerInlineBuiltin registers name as an inline builtin backed by handler.
// Every fake below shares this so the registration shape is asserted once.
func registerInlineBuiltin(name string, handler toolexec.Handler) func(*toolregistry.Registry, *toolexec.Handlers) error {
	return func(reg *toolregistry.Registry, h *toolexec.Handlers) error {
		def := &llmv1.ToolDefinition{
			Name:           name,
			Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
			Execution: &llmv1.ToolDefinition_InlineBuiltin{
				InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
			},
		}
		if err := reg.Register(def); err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
		return h.Register(name, handler)
	}
}

// boundWorkspace returns the workspace bound to this invocation, or an error.
// A handler that cannot name its workspace must fail rather than answer from a
// default partition — that is the invariant these fixtures exist to prove.
func boundWorkspace(inv toolexec.Invocation) (string, error) {
	ws := strings.TrimSpace(inv.Scope.TenantID)
	if ws == "" {
		return "", fmt.Errorf("no workspace bound to this invocation")
	}
	return ws, nil
}

type fakeKeyArgs struct {
	Key string `json:"key"`
}

// fakeRelatedPayload is an envelope with a status field plus a result list,
// enough to exercise dispatch and decoding.
type fakeRelatedPayload struct {
	Status      string   `json:"status"`
	WorkspaceID string   `json:"workspace_id"`
	Related     []string `json:"related"`
}

// registerFakeRelatedTool registers host_related, whose handler closes over
// relatedByWorkspace and resolves which workspace's data to answer from purely
// off inv.Scope.TenantID.
func registerFakeRelatedTool(relatedByWorkspace map[string]map[string][]string) func(*toolregistry.Registry, *toolexec.Handlers) error {
	return registerInlineBuiltin("host_related", func(_ context.Context, args json.RawMessage, inv toolexec.Invocation) (toolexec.Result, error) {
		ws, err := boundWorkspace(inv)
		if err != nil {
			return toolexec.Result{}, err
		}
		var parsed fakeKeyArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &parsed); err != nil {
				return toolexec.Result{}, fmt.Errorf("bad arguments: %w", err)
			}
		}
		b, err := json.Marshal(fakeRelatedPayload{
			Status:      "ready",
			WorkspaceID: ws,
			Related:     relatedByWorkspace[ws][parsed.Key],
		})
		if err != nil {
			return toolexec.Result{}, err
		}
		return toolexec.Result{Content: string(b)}, nil
	})
}

type fakeFindArgs struct {
	Needle string `json:"needle"`
}

type fakeFindHit struct {
	Key string `json:"key"`
}

type fakeFindPayload struct {
	Hits []fakeFindHit `json:"hits"`
}

// registerFakeFindTool registers host_find, resolving which workspace's entries
// to scan purely off inv.Scope.TenantID.
func registerFakeFindTool(entriesByWorkspace map[string]map[string]string) func(*toolregistry.Registry, *toolexec.Handlers) error {
	return registerInlineBuiltin("host_find", func(_ context.Context, args json.RawMessage, inv toolexec.Invocation) (toolexec.Result, error) {
		ws, err := boundWorkspace(inv)
		if err != nil {
			return toolexec.Result{}, err
		}
		var parsed fakeFindArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &parsed); err != nil {
				return toolexec.Result{}, fmt.Errorf("bad arguments: %w", err)
			}
		}
		var hits []fakeFindHit
		for key, body := range entriesByWorkspace[ws] {
			if parsed.Needle != "" && strings.Contains(body, parsed.Needle) {
				hits = append(hits, fakeFindHit{Key: key})
			}
		}
		b, err := json.Marshal(fakeFindPayload{Hits: hits})
		if err != nil {
			return toolexec.Result{}, err
		}
		return toolexec.Result{Content: string(b)}, nil
	})
}

// registerFakeFetchTool registers host_fetch, returning one stored entry from
// the workspace bound to the invocation.
func registerFakeFetchTool(entriesByWorkspace map[string]map[string]string) func(*toolregistry.Registry, *toolexec.Handlers) error {
	return registerInlineBuiltin("host_fetch", func(_ context.Context, args json.RawMessage, inv toolexec.Invocation) (toolexec.Result, error) {
		ws, err := boundWorkspace(inv)
		if err != nil {
			return toolexec.Result{}, err
		}
		var parsed fakeKeyArgs
		if err := json.Unmarshal(args, &parsed); err != nil {
			return toolexec.Result{}, err
		}
		body, ok := entriesByWorkspace[ws][parsed.Key]
		if !ok {
			return toolexec.Result{}, fmt.Errorf("host_fetch: %s: not found in workspace %s", parsed.Key, ws)
		}
		return toolexec.Result{Content: body}, nil
	})
}
