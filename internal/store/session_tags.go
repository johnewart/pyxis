package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/johnewart/pyxis/internal/serverutil"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Bounds on a single tag. Tags are labels for filtering, not a payload store —
// AttributesJSON already exists for arbitrary session data. Capping them keeps
// the scan that ListSessions performs over tags_json predictable.
const (
	MaxSessionTagKeyLen   = 128
	MaxSessionTagValueLen = 512
	MaxSessionTags        = 64
)

// ValidateSessionTagKey rejects keys that cannot be used safely or meaningfully.
//
// The double-quote rule is not cosmetic: the SQLite filter addresses a tag by
// JSON path (`$."key"`), so a key containing a quote could terminate the path
// early. The path is passed as a bound parameter rather than interpolated, so a
// bad key cannot inject SQL — but it could still silently match the wrong tag,
// which is worse than a clear error at write time.
func ValidateSessionTagKey(key string) error {
	k := strings.TrimSpace(key)
	switch {
	case k == "":
		return fmt.Errorf("tag key is required")
	case len(k) > MaxSessionTagKeyLen:
		return fmt.Errorf("tag key %q exceeds %d bytes", k, MaxSessionTagKeyLen)
	case strings.ContainsAny(k, "\"\\"):
		return fmt.Errorf("tag key %q may not contain quotes or backslashes", k)
	}
	return nil
}

// NormalizeSessionTags trims, validates and de-duplicates a tag map, returning
// the cleaned copy. A key trimming to empty is dropped rather than rejected so a
// caller can send a partially-filled form; genuinely malformed keys error.
func NormalizeSessionTags(tags map[string]string) (map[string]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if err := ValidateSessionTagKey(key); err != nil {
			return nil, err
		}
		if len(v) > MaxSessionTagValueLen {
			return nil, fmt.Errorf("tag %q value exceeds %d bytes", key, MaxSessionTagValueLen)
		}
		out[key] = strings.TrimSpace(v)
	}
	if len(out) > MaxSessionTags {
		return nil, fmt.Errorf("too many tags (%d), limit is %d", len(out), MaxSessionTags)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// EncodeSessionTags renders tags for the tags_json column. An empty map encodes
// as "" rather than "{}" so "no tags" has exactly one representation on disk
// (see SessionRow.TagsJSON). Keys are sorted so the column is byte-stable for a
// given map, which keeps test assertions and diffs sane.
func EncodeSessionTags(tags map[string]string) (string, error) {
	clean, err := NormalizeSessionTags(tags)
	if err != nil {
		return "", err
	}
	if len(clean) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(clean))
	for k := range clean {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kj, err := json.Marshal(k)
		if err != nil {
			return "", err
		}
		vj, err := json.Marshal(clean[k])
		if err != nil {
			return "", err
		}
		b.Write(kj)
		b.WriteByte(':')
		b.Write(vj)
	}
	b.WriteByte('}')
	return b.String(), nil
}

// DecodeSessionTags parses a tags_json value. Empty is no tags. A value that
// fails to parse yields no tags rather than an error: this column is read on
// every session list, and one corrupt row must not fail the whole page.
func DecodeSessionTags(raw string) map[string]string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeSessionTags applies an update to an existing tag set and returns the
// result. Keys present in `updates` are set; a key whose update value is empty
// is DELETED. This is merge-by-key, not wholesale replacement, so a caller
// setting one tag cannot clobber tags it never knew about.
func MergeSessionTags(existing, updates map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(updates))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range updates {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if strings.TrimSpace(v) == "" {
			delete(out, key)
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GetSessionTags returns the session's decoded tags (nil when untagged).
func (r *SessionRepo) GetSessionTags(ctx context.Context, sc serverutil.Scope, sessionID string) (map[string]string, error) {
	row, err := r.GetSession(ctx, sc, sessionID)
	if err != nil {
		return nil, err
	}
	return DecodeSessionTags(row.TagsJSON), nil
}

// UpdateSessionTags merges `updates` into the session's existing tags and returns
// the stored result. Keys with an empty value are removed (see MergeSessionTags).
//
// Read-modify-write inside a transaction with a row-level lock, because merge
// semantics are only safe if nothing else rewrites the column between the read
// and the write — two concurrent callers each setting a different tag must end up
// with both tags, not with one of them lost. The lock is skipped on SQLite, whose
// writes are already serialized and which does not implement SELECT ... FOR
// UPDATE.
func (r *SessionRepo) UpdateSessionTags(ctx context.Context, sc serverutil.Scope, sessionID string, updates map[string]string) (map[string]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("session repo not configured")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return nil, fmt.Errorf("workspace_id and session_id are required")
	}
	// Validate before opening the transaction so a bad key is a clean error.
	if _, err := NormalizeSessionTags(updates); err != nil {
		return nil, err
	}

	var merged map[string]string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&SessionRow{}).Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid)
		if tx.Dialector.Name() == "postgres" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var row SessionRow
		if err := q.First(&row).Error; err != nil {
			return err
		}
		merged = MergeSessionTags(DecodeSessionTags(row.TagsJSON), updates)
		encoded, err := EncodeSessionTags(merged)
		if err != nil {
			return err
		}
		res := tx.Model(&SessionRow{}).
			Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).
			Updates(map[string]any{"tags_json": encoded, "updated_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// ReplaceSessionTags overwrites the session's tags wholesale, dropping any key
// not present in `tags`. Separate from UpdateSessionTags because "set exactly
// these" and "merge these in" are different intents and a caller should have to
// pick one explicitly rather than discover which it got.
func (r *SessionRepo) ReplaceSessionTags(ctx context.Context, sc serverutil.Scope, sessionID string, tags map[string]string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("session repo not configured")
	}
	encoded, err := EncodeSessionTags(tags)
	if err != nil {
		return err
	}
	return r.UpdateSession(ctx, sc, sessionID, map[string]any{"tags_json": encoded})
}

// UpdateSessionChatSurface sets the session's surface independently of the rest
// of its metadata, so a caller can correct a surface without round-tripping (and
// risking overwriting) title/todos/attributes through SetSessionMeta.
func (r *SessionRepo) UpdateSessionChatSurface(ctx context.Context, sc serverutil.Scope, sessionID, surface string) error {
	s := strings.TrimSpace(surface)
	if s == "" {
		return fmt.Errorf("chat_surface is required")
	}
	return r.UpdateSession(ctx, sc, sessionID, map[string]any{"chat_surface": s})
}

// applySessionTagFilters ANDs one predicate per requested tag onto a query.
//
// `col` is the qualified tags_json column ("s.tags_json" for the aliased page
// query, "tags_json" for the unaliased count). An empty filter value means
// "key present with any value" rather than "value equals empty string" — the
// encoder never stores an empty value, so the two cannot be confused.
//
// Both dialects extract the value and compare it as text; the JSON key is a
// BOUND PARAMETER, never interpolated, so tag keys are not a SQL injection
// surface. NULLIF(col,”) guards the empty column: SQLite's json_extract errors
// on a non-JSON string and Postgres' ::jsonb cast fails on it, while both return
// NULL for a NULL input.
func applySessionTagFilters(db *gorm.DB, q *gorm.DB, col string, tags map[string]string) (*gorm.DB, error) {
	if len(tags) == 0 {
		return q, nil
	}
	clean, err := NormalizeSessionTags(tags)
	if err != nil {
		return nil, err
	}
	// Deterministic predicate order keeps generated SQL stable across calls.
	keys := make([]string, 0, len(clean))
	for k := range clean {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	postgres := db.Dialector.Name() == "postgres"
	for _, k := range keys {
		var expr string
		var pathArg any
		if postgres {
			expr = fmt.Sprintf("NULLIF(%s, '')::jsonb ->> ?", col)
			pathArg = k
		} else {
			expr = fmt.Sprintf("json_extract(NULLIF(%s, ''), ?)", col)
			pathArg = `$."` + k + `"`
		}
		if want := clean[k]; want == "" {
			q = q.Where(expr+" IS NOT NULL", pathArg)
		} else {
			q = q.Where(expr+" = ?", pathArg, want)
		}
	}
	return q, nil
}
