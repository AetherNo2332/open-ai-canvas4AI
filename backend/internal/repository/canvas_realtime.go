package repository

import (
	"context"
	"fmt"
	"strings"
)

type CanvasRealtimeKey struct {
	OwnerID  string
	CanvasID string
}
type CanvasCommittedState struct {
	OwnerID       string `json:"-"`
	CanvasID      string `json:"-"`
	Revision      int64  `json:"revision"`
	DocumentEpoch string `json:"documentEpoch"`
	EventSeq      int64  `json:"eventSeq"`
	Deleted       bool   `json:"deleted"`
}

// CanvasCommittedStates is a bounded, single-statement committed projection.
// The realtime hub pins this method to the construction-time base repository,
// never a writer/savepoint repository. No document payload is selected.
func (r *Repository) CanvasCommittedStates(ctx context.Context, keys []CanvasRealtimeKey) ([]CanvasCommittedState, error) {
	if len(keys) > 128 {
		return nil, fmt.Errorf("realtime observation batch exceeds 128")
	}
	if len(keys) == 0 {
		return []CanvasCommittedState{}, nil
	}
	projectTerms, epochTerms := []string{}, []string{}
	args := make([]any, 0, len(keys)*4)
	for _, key := range keys {
		projectTerms = append(projectTerms, "(c.user_id = ? AND c.id = ?)")
		args = append(args, key.OwnerID, key.CanvasID)
	}
	for _, key := range keys {
		epochTerms = append(epochTerms, "(e.owner_id = ? AND e.canvas_id = ?)")
		args = append(args, key.OwnerID, key.CanvasID)
	}
	projects, epochs := r.db.NamingStrategy.TableName("CanvasProject"), r.db.NamingStrategy.TableName("CanvasDocumentEpoch")
	query := fmt.Sprintf(`SELECT c.user_id AS owner_id,c.id AS canvas_id,c.revision,COALESCE(e.epoch,'') AS document_epoch,COALESCE(e.event_seq,0) AS event_seq,COALESCE(e.deleted,false) AS deleted
 FROM %s c LEFT JOIN %s e ON e.owner_id=c.user_id AND e.canvas_id=c.id WHERE %s
 UNION ALL SELECT e.owner_id,e.canvas_id,0 AS revision,e.epoch AS document_epoch,e.event_seq,true AS deleted FROM %s e
 WHERE (%s) AND NOT EXISTS (SELECT 1 FROM %s c WHERE c.user_id=e.owner_id AND c.id=e.canvas_id)`, projects, epochs, strings.Join(projectTerms, " OR "), epochs, strings.Join(epochTerms, " OR "), projects)
	rows := []CanvasCommittedState{}
	if err := r.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	byKey := map[CanvasRealtimeKey]CanvasCommittedState{}
	for _, row := range rows {
		byKey[CanvasRealtimeKey{OwnerID: row.OwnerID, CanvasID: row.CanvasID}] = row
	}
	out := make([]CanvasCommittedState, 0, len(keys))
	for _, key := range keys {
		row, exists := byKey[key]
		if !exists {
			row = CanvasCommittedState{OwnerID: key.OwnerID, CanvasID: key.CanvasID, Deleted: true}
		}
		out = append(out, row)
	}
	return out, nil
}
