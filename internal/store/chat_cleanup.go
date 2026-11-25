package store

import (
	"gorm.io/gorm"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// RemoveAllChatForSession deletes every persisted chat message row for a session
// within a workspace. Chat is session-native (keyed by workspace_id + session_id);
// used by SessionRepo.PurgeSession.
func RemoveAllChatForSession(db *gorm.DB, sc serverutil.Scope, sessionID string) error {
	if db == nil {
		return nil
	}
	if err := db.Where("tenant_id = ? AND scope_id = ? AND session_id = ?", sc.TenantID, sc.ScopeID, sessionID).
		Delete(&ChatMessageRow{}).Error; err != nil {
		return err
	}
	return refreshSessionLastMessageAt(db, sc, sessionID)
}
