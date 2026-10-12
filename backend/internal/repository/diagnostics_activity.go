package repository

import (
	"slices"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

type DiagnosticActivity struct {
	Canvases  []model.CanvasProject
	Snapshots []model.CanvasSnapshot
	Mutations []model.CloudAgentCanvasMutation
	Runs      []model.CloudAgentExecution
	Events    []model.CloudAgentEventRecord
	PiEntries []model.CloudAgentPiEntry
	Messages  []model.CloudAgentMessageRecord
	Truncated bool
}

const diagnosticJSONCharacterLimit = 64 << 10

// Latest rows are retained at every limit. JSON bodies are projected by the app;
// canvas payloads and mutation undo snapshots never enter the export collector.
func (r *Repository) DiagnosticActivity(userID string, from, to time.Time, canvasID string) (*DiagnosticActivity, error) {
	result := &DiagnosticActivity{}
	scoped := func() *gorm.DB {
		q := r.db.Where("user_id = ?", userID)
		if canvasID != "" {
			q = q.Where("canvas_id = ?", canvasID)
		}
		return q
	}
	canvases := r.db.Where("user_id = ?", userID)
	if canvasID != "" {
		canvases = canvases.Where("id = ?", canvasID)
	} else {
		canvases = canvases.Where("updated_at >= ? AND created_at <= ?", from, to)
	}
	if err := diagnosticRecentRows(canvases.Omit("PayloadJSON").Order("updated_at desc, id desc"), 100, &result.Canvases, &result.Truncated); err != nil {
		return nil, err
	}
	if err := diagnosticRecentRows(scoped().Where("created_at >= ? AND created_at <= ?", from, to).Omit("PayloadJSON").Order("created_at desc, id desc"), 200, &result.Snapshots, &result.Truncated); err != nil {
		return nil, err
	}
	if err := diagnosticRecentRows(scoped().Where("(created_at >= ? AND created_at <= ?) OR (undone_at >= ? AND undone_at <= ?)", from, to, from, to).Omit("BeforeJSON").Order("CASE WHEN undone_at > created_at THEN undone_at ELSE created_at END desc, id desc"), 200, &result.Mutations, &result.Truncated); err != nil {
		return nil, err
	}
	if err := diagnosticRecentRows(scoped().Where("created_at <= ? AND updated_at >= ?", to, from).Order("updated_at desc, id desc"), 50, &result.Runs, &result.Truncated); err != nil {
		return nil, err
	}
	if len(result.Runs) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(result.Runs))
	for _, run := range result.Runs {
		ids = append(ids, run.ID)
	}
	rows := func() *gorm.DB { return r.db.Where("user_id = ? AND run_id IN ?", userID, ids) }
	if err := diagnosticRecentRows(rows().Where("created_at >= ? AND created_at <= ?", from, to).Order("created_at desc, run_id desc, sequence desc"), 1000, &result.Events, &result.Truncated); err != nil {
		return nil, err
	}
	// Pi entries may embed images. Bound the raw JSON in SQL before it enters
	// process memory; an omitted body retains its identity as an unreadable row.
	piRows := rows().Select("session_id, sequence, entry_id, user_id, run_id, parent_id, created_at, CASE WHEN length(entry_json) <= ? THEN entry_json ELSE '' END AS entry_json", diagnosticJSONCharacterLimit)
	if err := diagnosticRecentRows(piRows.Where("created_at >= ? AND created_at <= ?", from, to).Order("created_at desc, session_id desc, sequence desc"), 1000, &result.PiEntries, &result.Truncated); err != nil {
		return nil, err
	}
	// Check existence outside the window too: an active run with only older
	// session messages must not reintroduce them via a transcript fallback.
	var sessionRunIDs []string
	if err := rows().Model(&model.CloudAgentPiEntry{}).Distinct("run_id").Pluck("run_id", &sessionRunIDs).Error; err != nil {
		return nil, err
	}
	legacyIDs, piIDs := make([]string, 0), make([]string, 0)
	for _, run := range result.Runs {
		if slices.Contains(sessionRunIDs, run.ID) {
			continue
		}
		if run.Engine == "pi" {
			piIDs = append(piIDs, run.ID)
		} else {
			legacyIDs = append(legacyIDs, run.ID)
		}
	}
	if len(legacyIDs)+len(piIDs) > 0 {
		query := r.db.Model(&model.CloudAgentMessageRecord{}).
			Select("run_id, kind, sequence, cloud_agent_message_records.user_id, CASE WHEN length(message_json) <= ? THEN message_json ELSE '' END AS message_json", diagnosticJSONCharacterLimit).
			Joins("JOIN cloud_agent_executions ON cloud_agent_executions.id = cloud_agent_message_records.run_id AND cloud_agent_executions.user_id = cloud_agent_message_records.user_id").
			Where("cloud_agent_message_records.user_id = ?", userID).Where("(run_id IN ? AND kind = ?) OR (run_id IN ? AND kind = ?)", legacyIDs, "canonical", piIDs, "pi")
		if err := diagnosticRecentRows(query.Order("cloud_agent_executions.updated_at desc, run_id desc, sequence desc"), 500, &result.Messages, &result.Truncated); err != nil {
			return nil, err
		}
	}
	for _, row := range result.PiEntries {
		if row.EntryJSON == "" {
			result.Truncated = true
		}
	}
	for _, row := range result.Messages {
		if row.MessageJSON == "" {
			result.Truncated = true
		}
	}
	return result, nil
}

func diagnosticRecentRows[T any](query *gorm.DB, limit int, dest *[]T, truncated *bool) error {
	if err := query.Limit(limit + 1).Find(dest).Error; err != nil {
		return err
	}
	if len(*dest) > limit {
		*dest = (*dest)[:limit]
		*truncated = true
	}
	slices.Reverse(*dest)
	return nil
}
