package toolregistry

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/protobuf/proto"
)

// Registry stores ToolDefinition values keyed by name for use on LLM requests.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]*llmv1.ToolDefinition
}

func New() *Registry {
	return &Registry{byName: make(map[string]*llmv1.ToolDefinition)}
}

// Register upserts a tool by name.
func (r *Registry) Register(t *llmv1.ToolDefinition) error {
	if t == nil {
		return fmt.Errorf("tool is nil")
	}
	name := strings.TrimSpace(t.GetName())
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	cp := proto.Clone(t).(*llmv1.ToolDefinition)
	cp.Name = name
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byName == nil {
		r.byName = make(map[string]*llmv1.ToolDefinition)
	}
	r.byName[name] = cp
	return nil
}

// Unregister removes a tool by name. It returns false if the tool was not present.
func (r *Registry) Unregister(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byName == nil {
		return false
	}
	if _, ok := r.byName[name]; !ok {
		return false
	}
	delete(r.byName, name)
	return true
}

// Get returns a clone of the registered tool definition for name, if present.
// The returned value is independent of the registry (same cloning contract as Snapshot).
func (r *Registry) Get(name string) (*llmv1.ToolDefinition, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.byName == nil {
		return nil, false
	}
	t, ok := r.byName[name]
	if !ok || t == nil {
		return nil, false
	}
	return proto.Clone(t).(*llmv1.ToolDefinition), true
}

// Snapshot returns a stable-ordered copy of registered tools (including execution bindings).
func (r *Registry) Snapshot() []*llmv1.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.byName) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*llmv1.ToolDefinition, 0, len(names))
	for _, n := range names {
		t := r.byName[n]
		if t == nil {
			continue
		}
		out = append(out, proto.Clone(t).(*llmv1.ToolDefinition))
	}
	return out
}
