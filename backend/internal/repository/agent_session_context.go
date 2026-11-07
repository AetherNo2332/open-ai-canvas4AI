package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

// AppendAgentSessionMessages appends messages and advances the revision in one
// transaction. The revision bump is conditional on the live writer (owner,
// epoch, unexpired lease) and the expected revision, and runs first so the
// row lock serializes appends on PostgreSQL and SQLite alike. Message IDs that
// already exist are skipped; when every message is a replay nothing changes.
func (r *Repository) AppendAgentSessionMessages(ownerID, sessionID, writer string, epoch, expectedRevision int64, now time.Time, messages []model.AgentSessionMessage) (int64, error) {
	revision := expectedRevision
	err := r.db.Transaction(func(tx *gorm.DB) error {
		fresh := []model.AgentSessionMessage{}
		for _, message := range messages {
			var count int64
			if err := tx.Model(&model.AgentSessionMessage{}).Where("owner_id = ? AND session_id = ? AND message_id = ?", ownerID, sessionID, message.MessageID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				fresh = append(fresh, message)
			}
		}
		if len(fresh) == 0 {
			return nil
		}
		bumped := tx.Model(&model.AgentSession{}).
			Where("id = ? AND owner_id = ? AND status = ? AND lease_owner = ? AND lease_epoch = ? AND lease_expires_at > ? AND revision = ?", sessionID, ownerID, "active", writer, epoch, now, expectedRevision).
			Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now})
		if err := agentSessionSwapResult(bumped); err != nil {
			return err
		}
		var last int64
		if err := tx.Model(&model.AgentSessionMessage{}).Where("session_id = ?", sessionID).Select("COALESCE(MAX(sequence), 0)").Scan(&last).Error; err != nil {
			return err
		}
		for index := range fresh {
			fresh[index].OwnerID, fresh[index].SessionID, fresh[index].Sequence, fresh[index].CreatedAt = ownerID, sessionID, last+int64(index)+1, now
		}
		if err := tx.Create(&fresh).Error; err != nil {
			return err
		}
		revision = expectedRevision + 1
		return nil
	})
	return revision, err
}

// AppendAgentSessionMessageFromPi projects one assistant result from the
// business-neutral Pi writer into the public Session history. The Pi session
// lease is the single-writer fence for this path; AgentSession's revision keeps
// the user-facing sequence ordered and the MessageID makes a retry a no-op.
func (r *Repository) AppendAgentSessionMessageFromPi(ownerID, sessionID, conversationID, runID, leaseOwner string, leaseEpoch int64, message model.AgentSessionMessage, now time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var piSession model.CloudAgentPiSession
		if err := tx.Where("user_id = ? AND conversation_id = ? AND active_run_id = ? AND lease_owner = ? AND lease_epoch = ? AND lease_expires_at > ?", ownerID, conversationID, runID, leaseOwner, leaseEpoch, now).First(&piSession).Error; err != nil {
			return err
		}
		var existing model.AgentSessionMessage
		err := tx.Where("owner_id = ? AND session_id = ? AND message_id = ?", ownerID, sessionID, message.MessageID).First(&existing).Error
		if err == nil {
			if existing.Body != message.Body || existing.RunID != runID {
				return ErrCreationConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var session model.AgentSession
		if err := tx.Where("id = ? AND owner_id = ? AND status = ?", sessionID, ownerID, "active").First(&session).Error; err != nil {
			return err
		}
		bumped := tx.Model(&model.AgentSession{}).Where("id = ? AND owner_id = ? AND status = ? AND revision = ?", sessionID, ownerID, "active", session.Revision).
			Updates(map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": now})
		if bumped.Error != nil {
			return bumped.Error
		}
		if bumped.RowsAffected != 1 {
			return ErrAgentSessionChanged
		}
		var sequence int64
		if err := tx.Model(&model.AgentSessionMessage{}).Where("session_id = ?", sessionID).Select("COALESCE(MAX(sequence), 0)").Scan(&sequence).Error; err != nil {
			return err
		}
		message.OwnerID, message.SessionID, message.Sequence, message.CreatedAt, message.RunID = ownerID, sessionID, sequence+1, now, runID
		return tx.Create(&message).Error
	})
}

// AgentSessionMessages returns the Session history in order, owner-scoped.
func (r *Repository) AgentSessionMessages(ownerID, sessionID string) ([]model.AgentSessionMessage, error) {
	rows := []model.AgentSessionMessage{}
	err := r.db.Where("owner_id = ? AND session_id = ?", ownerID, sessionID).Order("sequence ASC").Find(&rows).Error
	return rows, err
}

// AgentContextNotes returns exactly the notes a Session may read: its own
// session notes, documents of its exact space, and the owner's preferences.
func (r *Repository) AgentContextNotes(ownerID, sessionID, application, spaceType, spaceID string) ([]model.AgentMemoryNote, error) {
	rows := []model.AgentMemoryNote{}
	err := r.db.Where("owner_id = ?", ownerID).
		Where("(kind = 'session' AND session_id = ?) OR (kind = 'business_space' AND application = ? AND space_type = ? AND space_id = ?) OR kind = 'user_preference'", sessionID, application, spaceType, spaceID).
		Order("created_at ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (r *Repository) CreateAgentMemoryNote(note *model.AgentMemoryNote) error {
	return r.db.Create(note).Error
}

func (r *Repository) AgentMemoryNoteForOwner(ownerID, id string) (*model.AgentMemoryNote, error) {
	var note model.AgentMemoryNote
	if err := r.db.First(&note, "id = ? AND owner_id = ?", id, ownerID).Error; err != nil {
		return nil, err
	}
	return &note, nil
}

// PublishAgentMemoryNote inserts the space document for a session note, or
// returns the document an earlier publish created.
func (r *Repository) PublishAgentMemoryNote(document *model.AgentMemoryNote) (*model.AgentMemoryNote, error) {
	created := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(document)
	if created.Error != nil {
		return nil, created.Error
	}
	if created.RowsAffected == 1 {
		return document, nil
	}
	var stored model.AgentMemoryNote
	err := r.db.First(&stored, "owner_id = ? AND published_from = ?", document.OwnerID, *document.PublishedFrom).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentSessionChanged
	}
	return &stored, err
}
