package providers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnewart/pyxis/internal/llm/backend"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

const compositeLogModule = "providers"

// Composite merges an optional in-process Registry with provider rows from the database.
// Production wires Global as nil: every provider is a DB row registered by a client over
// gRPC via RegisterLlmProvider, keyed by (tenant_id, scope_id, provider_id). Only tests
// supply a Registry, which is unscoped and consulted only when no DB row resolves.
//
// Resolution for a serverutil.Scope{TenantID, ScopeID} prefers the row registered for that
// exact scope and falls back to the tenant-wide row (empty scope_id) of the same tenant.
// Tenants never see each other's rows. Resolved entries are cached per (scope, provider id);
// any write for a provider id evicts every cached entry for that tenant and id, because a
// scoped row shadows the tenant-wide row other scopes may have cached.
type Composite struct {
	Global *Registry
	DB     *store.LlmProviderRepo

	// Models is the durable per-provider catalog cache. NewComposite derives it
	// from DB, so any Composite that can resolve DB-backed providers also
	// persists their catalogs — persistence is not something a host opts into.
	// It is nil ONLY for a Composite with no DB at all (registry-only unit
	// tests), where there is no database to write to.
	Models *store.LlmProviderModelRepo

	mu    sync.Mutex
	cache map[cacheKey]*Entry
}

// cacheKey identifies one resolved entry: the requesting scope plus provider id.
type cacheKey struct {
	Scope      serverutil.Scope
	ProviderID string
}

// NewComposite builds a resolver. Either side may be nil; production passes global=nil.
//
// The durable model catalog is derived from user rather than being a field the
// caller must remember to set afterwards: sync writing to the database is part
// of what sync means, not a host-configurable extra.
func NewComposite(global *Registry, user *store.LlmProviderRepo) *Composite {
	return &Composite{
		Global: global,
		DB:     user,
		Models: user.ModelCatalog(),
		cache:  make(map[cacheKey]*Entry),
	}
}

// DefaultID returns the default provider id from the in-process global Registry.
// That registry is nil in production, so this is normally empty.
func (c *Composite) DefaultID() string {
	if c == nil || c.Global == nil {
		return ""
	}
	return c.Global.DefaultID()
}

// DefaultBackend returns the backend for DefaultID() (global only).
func (c *Composite) DefaultBackend() backend.Backend {
	if c == nil || c.Global == nil {
		return nil
	}
	return c.Global.DefaultBackend()
}

func (c *Composite) invalidate(sc serverutil.Scope, pid string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pid = strings.TrimSpace(pid)
	if pid == "" {
		c.cache = make(map[cacheKey]*Entry)
		return
	}
	// Evict tenant-wide: a scoped write changes what sibling and tenant-wide
	// lookups for the same id resolve to.
	for k := range c.cache {
		if k.ProviderID == pid && k.Scope.TenantID == sc.TenantID {
			delete(c.cache, k)
		}
	}
}

// InvalidateUserProvider drops every cached entry for providerID in sc's tenant after
// register/unregister or a DB change. An empty providerID clears the whole cache.
func (c *Composite) InvalidateUserProvider(sc serverutil.Scope, providerID string) {
	c.invalidate(sc, providerID)
}

func (c *Composite) userCacheGet(sc serverutil.Scope, id string) *Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cache[cacheKey{Scope: sc, ProviderID: id}]
}

func (c *Composite) userCacheSet(sc serverutil.Scope, id string, e *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = make(map[cacheKey]*Entry)
	}
	c.cache[cacheKey{Scope: sc, ProviderID: id}] = e
}

func (c *Composite) loadUserEntry(ctx context.Context, sc serverutil.Scope, id string) (*Entry, bool) {
	id = strings.TrimSpace(id)
	if c == nil || c.DB == nil || id == "" {
		return nil, false
	}
	if e := c.userCacheGet(sc, id); e != nil {
		return e, true
	}
	cfg, err := c.DB.Get(ctx, sc, id)
	if err != nil {
		llmdlog.Stdout(compositeLogModule, llmdlog.LevelWarn,
			"provider %q (%s): loading DB row failed: %v", id, sc, err)
		return nil, false
	}
	if cfg == nil {
		return nil, false
	}
	e, err := BuildEntryFromProviderConfig(cfg)
	if err != nil || e == nil {
		llmdlog.Stdout(compositeLogModule, llmdlog.LevelWarn,
			"provider %q (%s): building entry failed: %v", id, sc, err)
		return nil, false
	}
	// Hydrate the catalog from the durable cache BEFORE publishing the entry.
	// Without this a fresh Entry always starts with an empty catalog, so a
	// restart (or any InvalidateUserProvider — i.e. editing any provider field)
	// silently emptied the model list and consumers fell back to showing the
	// provider id as if it were a model. A scan is never triggered here: reads
	// must stay cheap, which is the whole reason the cache exists.
	c.hydrateCatalog(ctx, sc, id, e)
	c.userCacheSet(sc, id, e)
	return e, true
}

// hydrateCatalog loads a provider's persisted models into e. Best-effort: a
// missing/failing cache degrades to an empty in-memory catalog (what callers
// got before persistence existed) rather than failing provider resolution and
// taking chat down with it.
func (c *Composite) hydrateCatalog(ctx context.Context, sc serverutil.Scope, providerID string, e *Entry) {
	if c == nil || c.Models == nil || e == nil {
		return
	}
	rows, err := c.Models.List(ctx, sc, providerID)
	if err != nil {
		llmdlog.Stdout(compositeLogModule, llmdlog.LevelWarn,
			"provider %q (%s): loading persisted catalog failed: %v", providerID, sc, err)
		return
	}
	if len(rows) == 0 {
		return
	}
	models := make([]CatalogModel, 0, len(rows))
	var newest int64
	for _, row := range rows {
		models = append(models, CatalogModel{
			ProviderID: providerID,
			ModelID:    row.ModelKey,
			Display:    row.Label,
		})
		if ms := row.DiscoveredAt.UTC().UnixMilli(); ms > newest {
			newest = ms
		}
	}
	// Report the persisted discovery time, not now(): callers use
	// models_refreshed_unix_ms to decide whether a catalog is stale, and
	// restamping it here would make every restart look like a fresh scan.
	e.replaceModels(models, newest)
}

// Get resolves a provider by id: the global DB row first, then the in-process global Registry
// (tests only) when no DB row exists.
func (c *Composite) Get(ctx context.Context, sc serverutil.Scope, id string) (*Entry, bool) {
	if c == nil {
		return nil, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false
	}
	if e, ok := c.loadUserEntry(ctx, sc, id); ok {
		return e, true
	}
	if c.Global != nil {
		return c.Global.Get(id)
	}
	return nil, false
}

// Backend returns the llm backend for a resolved provider id.
func (c *Composite) Backend(ctx context.Context, sc serverutil.Scope, id string) (backend.Backend, bool) {
	e, ok := c.Get(ctx, sc, id)
	if !ok || e == nil {
		return nil, false
	}
	return e.Backend, true
}

// ListMerged returns DB-registered providers first, then global Registry providers whose ids are
// not shadowed by a DB row.
func (c *Composite) ListMerged(ctx context.Context, sc serverutil.Scope) []ListProvidersView {
	var out []ListProvidersView
	userSet := map[string]struct{}{}

	if c != nil && c.DB != nil {
		ids, err := c.DB.ListIDs(ctx, sc)
		if err != nil {
			llmdlog.Stdout(compositeLogModule, llmdlog.LevelWarn,
				"listing DB providers (%s) failed: %v", sc, err)
		}
		sort.Strings(ids)
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			e, ok := c.loadUserEntry(ctx, sc, id)
			if !ok || e == nil {
				continue
			}
			userSet[id] = struct{}{}
			out = append(out, ListProvidersView{ID: id, UserRegistered: true, Entry: e})
		}
	}

	if c != nil && c.Global != nil {
		for _, id := range c.Global.OrderedIDs() {
			if _, shadowed := userSet[id]; shadowed {
				continue
			}
			e, ok := c.Global.Get(id)
			if !ok || e == nil {
				continue
			}
			out = append(out, ListProvidersView{ID: id, UserRegistered: false, Entry: e})
		}
	}

	return out
}

// ListProvidersView is one row for RPC listing (merged global DB + in-process Registry).
type ListProvidersView struct {
	ID             string
	UserRegistered bool
	Entry          *Entry
}

// RefreshModels runs catalog discovery for the resolved provider (DB or in-process Registry).
func (c *Composite) RefreshModels(ctx context.Context, sc serverutil.Scope, providerID string) (int, error) {
	if c == nil {
		return 0, fmt.Errorf("nil composite")
	}
	pid := strings.TrimSpace(providerID)
	if pid == "" {
		return 0, fmt.Errorf("provider id is required")
	}
	e, ok := c.Get(ctx, sc, pid)
	if !ok || e == nil {
		return 0, fmt.Errorf("unknown provider %q", pid)
	}
	models, err := e.refreshModels(ctx)
	if err != nil {
		// Deliberately no persistence on failure: a failed scan means "unknown",
		// not "none". Clearing here would let one flaky network call or CLI
		// hiccup wipe a good catalog.
		return 0, err
	}
	now := time.Now()
	e.replaceModels(models, now.UnixMilli())
	// Persist so the catalog survives restart and cache invalidation. A write
	// failure is not fatal — the in-memory catalog above is already correct for
	// this process, and failing the RPC would make a working scan look broken —
	// but it is always logged: a silently dropped write is exactly how a sync
	// that "worked" in the UI ended up with an empty table after restart.
	if c.Models == nil {
		llmdlog.Stdout(compositeLogModule, llmdlog.LevelError,
			"provider %q: scanned %d models but no catalog store is configured; catalog is memory-only and will be lost on restart",
			pid, len(models))
		return len(models), nil
	}
	rows := make([]store.ProviderModel, 0, len(models))
	for _, m := range models {
		rows = append(rows, store.ProviderModel{
			ModelKey:     m.ModelID,
			Label:        m.Display,
			DiscoveredAt: now,
		})
	}
	if err := c.Models.Replace(ctx, sc, pid, rows, now); err != nil {
		llmdlog.Stdout(compositeLogModule, llmdlog.LevelError,
			"provider %q: persisting %d scanned models failed; catalog will be lost on restart: %v",
			pid, len(models), err)
	}
	return len(models), nil
}

// DeleteModels drops a provider's persisted catalog. Called when a provider is
// unregistered so its models don't outlive it and get hydrated onto a
// re-registered provider of a different kind.
func (c *Composite) DeleteModels(ctx context.Context, sc serverutil.Scope, providerID string) error {
	if c == nil || c.Models == nil {
		return nil
	}
	return c.Models.Delete(ctx, sc, providerID)
}

// ListCatalog returns cached catalog entries, optionally filtered by provider id.
func (c *Composite) ListCatalog(ctx context.Context, sc serverutil.Scope, filter string) []CatalogModel {
	if c == nil {
		return nil
	}
	pf := strings.TrimSpace(filter)
	merged := c.ListMerged(ctx, sc)
	var out []CatalogModel
	for _, row := range merged {
		if pf != "" && row.ID != pf {
			continue
		}
		if row.Entry == nil {
			continue
		}
		out = append(out, row.Entry.SnapshotCatalog()...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}
