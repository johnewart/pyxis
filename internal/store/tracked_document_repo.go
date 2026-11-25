package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

// ErrTrackedDocInvalidArgs is returned when required identifiers are missing.
var ErrTrackedDocInvalidArgs = errors.New("tracked document: sc.TenantID, sc.ScopeID, sessionID and documentID are required")

// TrackedDocumentRepo appends and queries immutable tracked-document snapshots
// (llmd_tracked_document_versions). It is the ONLY write/read path for those rows;
// A client reaches them exclusively over gRPC and never opens llmd's SQLite directly.
type TrackedDocumentRepo struct{ db *gorm.DB }

// NewTrackedDocumentRepo returns a repo, or nil when db is nil.
func NewTrackedDocumentRepo(db *gorm.DB) *TrackedDocumentRepo {
	if db == nil {
		return nil
	}
	return &TrackedDocumentRepo{db: db}
}

// TrackedDocInput is one document snapshot to attach at a turn. Content is the full
// current body (frontmatter included) as of "when the user hit enter".
type TrackedDocInput struct {
	DocumentID      string
	Name            string
	Content         string
	AnchorTurnID    string
	AnchorMessageID string
}

// TrackedDocContentHash is the canonical content hash used for version dedup.
// Exported so send-time assembly can compare an incoming snapshot to the latest
// stored version without a second hashing convention drifting into existence.
func TrackedDocContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// LatestVersion returns the highest-seq version for a document, or (zero, false, nil)
// when the document has no versions yet.
func (r *TrackedDocumentRepo) LatestVersion(ctx context.Context, sc serverutil.Scope, sessionID, documentID string) (TrackedDocumentVersionRow, bool, error) {
	if r == nil || r.db == nil {
		return TrackedDocumentVersionRow{}, false, nil
	}
	sid := strings.TrimSpace(sessionID)
	doc := strings.TrimSpace(documentID)
	if sc.TenantID == "" || sid == "" || doc == "" {
		return TrackedDocumentVersionRow{}, false, ErrTrackedDocInvalidArgs
	}
	var row TrackedDocumentVersionRow
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND document_id = ?", sc.TenantID, sc.ScopeID, sid, doc).
		Order("seq DESC").Limit(1).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TrackedDocumentVersionRow{}, false, nil
	}
	if err != nil {
		return TrackedDocumentVersionRow{}, false, err
	}
	return row, true, nil
}

// AppendVersion appends a new snapshot for a document IFF its content changed since
// the latest stored version. Hash-dedup: identical content is a no-op that returns the
// existing latest row with created=false. This is the linchpin that keeps a client
// stateless — it may send the current content every turn and llmd decides whether a
// new version (and later a diff on the wire) actually materializes.
func (r *TrackedDocumentRepo) AppendVersion(ctx context.Context, sc serverutil.Scope, sessionID string, in TrackedDocInput) (TrackedDocumentVersionRow, bool, error) {
	if r == nil || r.db == nil {
		return TrackedDocumentVersionRow{}, false, nil
	}
	sid := strings.TrimSpace(sessionID)
	doc := strings.TrimSpace(in.DocumentID)
	if sc.TenantID == "" || sid == "" || doc == "" {
		return TrackedDocumentVersionRow{}, false, ErrTrackedDocInvalidArgs
	}
	hash := TrackedDocContentHash(in.Content)

	var (
		result  TrackedDocumentVersionRow
		created bool
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var latest TrackedDocumentVersionRow
		lerr := tx.Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND document_id = ?", sc.TenantID, sc.ScopeID, sid, doc).
			Order("seq DESC").Limit(1).First(&latest).Error
		hasLatest := lerr == nil
		if lerr != nil && !errors.Is(lerr, gorm.ErrRecordNotFound) {
			return lerr
		}
		// Unchanged since the latest version: no new row, no anchor rewrite.
		if hasLatest && latest.ContentHash == hash {
			result = latest
			created = false
			return nil
		}
		nextSeq := 0
		if hasLatest {
			nextSeq = latest.Seq + 1
		}
		row := TrackedDocumentVersionRow{
			TenantID:        sc.TenantID,
			ScopeID:         sc.ScopeID,
			SessionID:       sid,
			DocumentID:      doc,
			Seq:             nextSeq,
			AnchorTurnID:    strings.TrimSpace(in.AnchorTurnID),
			AnchorMessageID: strings.TrimSpace(in.AnchorMessageID),
			Name:            strings.TrimSpace(in.Name),
			ContentHash:     hash,
			Content:         in.Content,
			CreatedAt:       time.Now().UTC(),
		}
		if cerr := tx.Create(&row).Error; cerr != nil {
			return cerr
		}
		result = row
		created = true
		return nil
	})
	if err != nil {
		return TrackedDocumentVersionRow{}, false, err
	}
	return result, created, nil
}

// ListVersions returns all versions for a document, oldest first (seq ascending).
func (r *TrackedDocumentRepo) ListVersions(ctx context.Context, sc serverutil.Scope, sessionID, documentID string) ([]TrackedDocumentVersionRow, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	sid := strings.TrimSpace(sessionID)
	doc := strings.TrimSpace(documentID)
	if sc.TenantID == "" || sid == "" || doc == "" {
		return nil, ErrTrackedDocInvalidArgs
	}
	var rows []TrackedDocumentVersionRow
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ? AND document_id = ?", sc.TenantID, sc.ScopeID, sid, doc).
		Order("seq ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListSessionVersions returns every tracked-document version for a session, ordered by
// document then seq. Used by send-time assembly to inject all in-scope documents.
func (r *TrackedDocumentRepo) ListSessionVersions(ctx context.Context, sc serverutil.Scope, sessionID string) ([]TrackedDocumentVersionRow, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return nil, nil
	}
	var rows []TrackedDocumentVersionRow
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).
		Order("document_id ASC, seq ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// DeleteBySession removes all tracked-document versions for a session (cascade on
// session delete/purge).
func (r *TrackedDocumentRepo) DeleteBySession(ctx context.Context, sc serverutil.Scope, sessionID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).
		Delete(&TrackedDocumentVersionRow{}).Error
}
