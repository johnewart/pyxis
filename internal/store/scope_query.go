package store

import (
	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// scoped restricts a query to exactly one (tenant, scope) pair.
//
// It deliberately uses a string condition rather than a struct condition:
// GORM drops zero-valued fields from struct conditions, so
// Where(&Row{TenantID: t, ScopeID: ""}) would match every scope in the
// tenant. The tenant-wide scope (ScopeID == "") must match only rows whose
// scope_id is literally ”.
func scoped(db *gorm.DB, sc serverutil.Scope) *gorm.DB {
	return db.Where("tenant_id = ? AND scope_id = ?", sc.TenantID, sc.ScopeID)
}

// scopedOrTenantWide restricts a READ to rows in sc's exact scope plus the
// tenant-wide rows (scope_id = ”) of the same tenant. A tenant-wide caller
// (ScopeID == "") therefore matches only tenant-wide rows.
//
// Like scoped, it uses a string condition, never a struct condition.
//
// This can return both a scoped row and a tenant-wide row with the same id.
// Callers must still pick the scoped row over the tenant-wide one, for example
// by ordering on "scope_id DESC" and taking the first, or by deduplicating.
// Writes and deletes must use scoped, never this helper.
func scopedOrTenantWide(db *gorm.DB, sc serverutil.Scope) *gorm.DB {
	return db.Where("tenant_id = ? AND scope_id IN (?, '')", sc.TenantID, sc.ScopeID)
}
