package agentprofile

import (
	"context"
	"errors"
	"strings"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// ErrNotFound is returned when no tier holds the requested profile.
var ErrNotFound = errors.New("agent profile not found")

// DynamicStore is the per-scope (persisted) profile tier.
//
// Contract: Get MUST return an error satisfying errors.Is(err, ErrNotFound)
// when no row exists for (scope, id) — the store-backed implementation
// maps its driver's not-found sentinel to ErrNotFound. Any other error is
// propagated unchanged by the Resolver; nothing here swallows it. This
// package never imports the store or gorm; the dependency points the other
// way.
type DynamicStore interface {
	Get(ctx context.Context, sc serverutil.Scope, id string) (*Profile, error)
	List(ctx context.Context, sc serverutil.Scope) ([]*Profile, error)
}

// Resolver answers profile lookups across the static and dynamic tiers with
// static-first precedence: a static id shadows a dynamic profile of the same
// id. Dynamic is consulted only when it is wired and the scope has a tenant;
// without one, only the static tier answers.
type Resolver struct {
	Static  *Registry
	Dynamic DynamicStore
}

// NewResolver builds a resolver; a nil static registry falls back to Default.
func NewResolver(static *Registry, dynamic DynamicStore) *Resolver {
	if static == nil {
		static = Default
	}
	return &Resolver{Static: static, Dynamic: dynamic}
}

func (r *Resolver) useDynamic(sc serverutil.Scope) bool {
	return r != nil && r.Dynamic != nil && strings.TrimSpace(sc.TenantID) != ""
}

// Get returns the profile for id, static tier first. Returns ErrNotFound
// when neither tier has it; any other dynamic error propagates unchanged.
func (r *Resolver) Get(ctx context.Context, sc serverutil.Scope, id string) (*Profile, error) {
	if r == nil {
		return nil, ErrNotFound
	}
	id = strings.TrimSpace(id)
	if p, ok := r.Static.Get(id); ok {
		return p, nil
	}
	if !r.useDynamic(sc) {
		return nil, ErrNotFound
	}
	p, err := r.Dynamic.Get(ctx, sc, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	return p, nil
}

// List returns static profiles (sorted by id) followed by the scope's
// dynamic profiles. A dynamic profile whose id is shadowed by a static one is
// omitted so the list matches what Get would resolve. Dynamic errors
// propagate unchanged.
func (r *Resolver) List(ctx context.Context, sc serverutil.Scope) ([]*Profile, error) {
	if r == nil {
		return nil, nil
	}
	out := r.Static.List()
	if !r.useDynamic(sc) {
		return out, nil
	}
	dyn, err := r.Dynamic.List(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, p := range dyn {
		if p == nil {
			continue
		}
		if r.IsStatic(p.ID) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// IsStatic reports whether id belongs to the static tier.
func (r *Resolver) IsStatic(id string) bool {
	if r == nil {
		return false
	}
	_, ok := r.Static.Get(id)
	return ok
}
