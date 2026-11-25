package agentprofile

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry holds static (code-defined) profiles. Static profiles are
// read-only: they win over any dynamic profile with the same id and the CRUD
// RPCs refuse to modify them.
//
// The zero value is not usable; construct with NewRegistry. All methods are
// nil-receiver safe so a host that never wires a registry still gets sane
// behaviour (no profiles, registration refused).
type Registry struct {
	mu   sync.RWMutex
	byID map[string]*Profile
}

// NewRegistry returns an empty registry. Tests use this rather than Default so
// state never leaks across tests.
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]*Profile)}
}

// Default is the process-wide static registry that RegisterStatic feeds.
var Default = NewRegistry()

// RegisterStatic registers code-defined profiles into Default. It returns an
// error rather than panicking so hosts can decide how to fail (typically
// log.Fatalf at startup).
func RegisterStatic(defs ...Profile) error { return Default.Register(defs...) }

// Register validates and stores copies of defs, marking each Static=true.
// Registration is all-or-nothing: an empty id, a duplicate id within the
// call, a duplicate against an existing entry, or a Validate failure leaves
// the registry exactly as it was.
func (r *Registry) Register(defs ...Profile) error {
	if r == nil {
		return errors.New("agent profile registry: nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID == nil {
		r.byID = make(map[string]*Profile)
	}

	pending := make(map[string]*Profile, len(defs))
	for i := range defs {
		p := defs[i].clone()
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			return fmt.Errorf("agent profile registry: definition %d has an empty id", i)
		}
		if err := Validate(p); err != nil {
			return err
		}
		if _, dup := pending[p.ID]; dup {
			return fmt.Errorf("agent profile registry: duplicate id %q in the same registration", p.ID)
		}
		if _, exists := r.byID[p.ID]; exists {
			return fmt.Errorf("agent profile registry: id %q is already registered", p.ID)
		}
		p.Static = true
		pending[p.ID] = p
	}
	for id, p := range pending {
		r.byID[id] = p
	}
	return nil
}

// Get returns a copy of the profile with the given (trimmed) id.
func (r *Registry) Get(id string) (*Profile, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[strings.TrimSpace(id)]
	if !ok {
		return nil, false
	}
	return p.clone(), true
}

// List returns copies of every registered profile, sorted by ID.
func (r *Registry) List() []*Profile {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*Profile, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.byID[id].clone())
	}
	return out
}
