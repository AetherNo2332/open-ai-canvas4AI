package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
)

const cloudAgentPiSessionFormatVersion = 3

// AttachCloudAgentPiSession creates the durable session for a new conversation or
// atomically assigns its next run. Sibling continuations racing from one parent cannot
// both become the active writer.
func (r *Repository) AttachCloudAgentPiSession(run *model.CloudAgentExecution) error {
	if run == nil || run.UserID == "" || run.ID == "" {
		return errors.New("Pi session run identity is incomplete")
	}
	sessionID := run.ConversationID
	if sessionID == "" {
		sessionID = run.ID
	}
	createdAt := run.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	header, err := json.Marshal(map[string]any{
		"type": "session", "version": cloudAgentPiSessionFormatVersion, "id": sessionID,
		"timestamp": createdAt.UTC().Format(time.RFC3339Nano), "cwd": "canvas://" + run.CanvasID,
	})
	if err != nil {
		return err
	}
	var session model.CloudAgentPiSession
	err = r.db.First(&session, "id = ? AND user_id = ?", sessionID, run.UserID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		session = model.CloudAgentPiSession{
			ID: sessionID, UserID: run.UserID, ConversationID: sessionID, CanvasID: run.CanvasID,
			FormatVersion: cloudAgentPiSessionFormatVersion, HeaderJSON: string(header), Revision: 1,
			ActiveRunID: run.ID, CreatedAt: createdAt, UpdatedAt: time.Now(),
		}
		created := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&session)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected != 0 {
			return nil
		}
		if err := r.db.First(&session, "id = ? AND user_id = ?", sessionID, run.UserID).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if session.CanvasID != run.CanvasID || session.ConversationID != sessionID {
		return fmt.Errorf("Pi session %q owner or canvas does not match run", sessionID)
	}
	if session.FormatVersion != cloudAgentPiSessionFormatVersion {
		return fmt.Errorf("unsupported Pi session format version %d", session.FormatVersion)
	}
	if session.ActiveRunID == run.ID {
		return nil
	}
	if session.ActiveRunID != "" && session.ActiveRunID != run.ParentID {
		return ErrCreationConflict
	}
	updated := r.db.Model(&model.CloudAgentPiSession{}).
		Where("id = ? AND user_id = ? AND canvas_id = ? AND revision = ? AND active_run_id = ?",
			sessionID, run.UserID, run.CanvasID, session.Revision, session.ActiveRunID).
		Updates(map[string]any{
			"active_run_id": run.ID,
			"revision":      gorm.Expr("revision + 1"),
			"updated_at":    time.Now(),
		})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}

// CloudAgentPiSession loads a session only in the authenticated user's scope.
func (r *Repository) CloudAgentPiSession(userID, sessionID string) (*model.CloudAgentPiSession, []model.CloudAgentPiEntry, error) {
	var session model.CloudAgentPiSession
	if err := r.db.First(&session, "id = ? AND user_id = ?", sessionID, userID).Error; err != nil {
		return nil, nil, err
	}
	entries := make([]model.CloudAgentPiEntry, 0)
	if err := r.db.Where("session_id = ? AND user_id = ?", sessionID, userID).Order("sequence ASC").Find(&entries).Error; err != nil {
		return nil, nil, err
	}
	return &session, entries, nil
}

// AppendCloudAgentPiSessionEntries appends a Pi entry batch and advances the active leaf
// under a session revision check. Identical retries are no-ops; an entry ID reused with a
// different body or a stale branch update is rejected.
func (r *Repository) AppendCloudAgentPiSessionEntries(userID, sessionID, runID string, expectedRevision int64, activeLeafID string, entries []model.CloudAgentPiEntry) (int64, error) {
	var session model.CloudAgentPiSession
	if err := r.db.First(&session, "id = ? AND user_id = ?", sessionID, userID).Error; err != nil {
		return 0, err
	}
	if session.FormatVersion != cloudAgentPiSessionFormatVersion || session.ActiveRunID != runID {
		return 0, ErrCreationConflict
	}
	if len(entries) > 128 {
		return 0, fmt.Errorf("Pi session append batch exceeds limit")
	}
	newEntries := make([]model.CloudAgentPiEntry, 0, len(entries))
	known := make(map[string]bool, len(entries))
	available := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.EntryID == "" || len(entry.EntryID) > 160 || entry.SessionID != sessionID || entry.UserID != userID || entry.RunID != runID || len(entry.EntryJSON) == 0 || !json.Valid([]byte(entry.EntryJSON)) {
			return 0, fmt.Errorf("Pi session entry identity is invalid")
		}
		if known[entry.EntryID] {
			return 0, fmt.Errorf("Pi session append contains duplicate entry IDs")
		}
		known[entry.EntryID] = true
		var existing model.CloudAgentPiEntry
		err := r.db.First(&existing, "session_id = ? AND user_id = ? AND entry_id = ?", sessionID, userID, entry.EntryID).Error
		if err == nil {
			if !sameJSONDocument(existing.EntryJSON, entry.EntryJSON) {
				return 0, ErrCreationConflict
			}
			available[entry.EntryID] = true
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		newEntries = append(newEntries, entry)
	}
	if len(newEntries) == 0 && activeLeafID == session.ActiveLeafID {
		return session.Revision, nil
	}
	if expectedRevision != session.Revision {
		return 0, ErrCreationConflict
	}
	var maxSequence int
	if err := r.db.Model(&model.CloudAgentPiEntry{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID).
		Select("COALESCE(MAX(sequence), 0)").Scan(&maxSequence).Error; err != nil {
		return 0, err
	}
	for index := range newEntries {
		entry := &newEntries[index]
		if entry.ParentID != "" && !available[entry.ParentID] {
			var parentCount int64
			if err := r.db.Model(&model.CloudAgentPiEntry{}).
				Where("session_id = ? AND user_id = ? AND entry_id = ?", sessionID, userID, entry.ParentID).
				Count(&parentCount).Error; err != nil {
				return 0, err
			}
			if parentCount != 1 {
				return 0, fmt.Errorf("Pi session entry parent does not exist")
			}
		}
		maxSequence++
		entry.Sequence = maxSequence
		entry.CreatedAt = time.Now()
		available[entry.EntryID] = true
	}
	if activeLeafID != "" && !available[activeLeafID] {
		var leafCount int64
		if err := r.db.Model(&model.CloudAgentPiEntry{}).
			Where("session_id = ? AND user_id = ? AND entry_id = ?", sessionID, userID, activeLeafID).
			Count(&leafCount).Error; err != nil {
			return 0, err
		}
		if leafCount != 1 {
			return 0, fmt.Errorf("Pi session active leaf does not exist")
		}
	}
	if len(newEntries) > 0 {
		if err := r.db.Create(&newEntries).Error; err != nil {
			return 0, err
		}
	}
	updated := r.db.Model(&model.CloudAgentPiSession{}).
		Where("id = ? AND user_id = ? AND revision = ? AND active_run_id = ?", sessionID, userID, expectedRevision, runID).
		Updates(map[string]any{"active_leaf_id": activeLeafID, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now()})
	if updated.Error != nil {
		return 0, updated.Error
	}
	if updated.RowsAffected != 1 {
		return 0, ErrCreationConflict
	}
	return expectedRevision + 1, nil
}
