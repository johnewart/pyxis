package store

import (
	"fmt"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"

	"gorm.io/gorm"
)

// ConfirmChatSessionExists returns the session id when persisted chat rows exist
// for (scope, sessionID), or gorm.ErrRecordNotFound otherwise.
func ConfirmChatSessionExists(db *gorm.DB, sc serverutil.Scope, sessionID string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database is required")
	}
	sid := strings.TrimSpace(sessionID)
	if sc.TenantID == "" || sid == "" {
		return "", fmt.Errorf("tenant_id and session_id are required")
	}
	var count int64
	if err := db.Model(&ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sid).
		Count(&count).Error; err != nil {
		return "", err
	}
	if count == 0 {
		return "", gorm.ErrRecordNotFound
	}
	return sid, nil
}
