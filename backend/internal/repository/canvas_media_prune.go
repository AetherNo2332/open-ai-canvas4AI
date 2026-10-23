package repository

import (
	"encoding/json"
	"strings"

	"infinite-canvas/backend/internal/model"
)

type mediaRootNode struct {
	ID       string `json:"id"`
	Metadata struct {
		Version struct {
			Epoch      string `json:"epoch"`
			Generation int64  `json:"generation"`
		} `json:"mediaVersion"`
	} `json:"metadata"`
}

func (r *Repository) canvasMediaVersionRoots(owner, canvasID string) (map[mediaVersionRoot]bool, error) {
	roots := map[mediaVersionRoot]bool{}
	collect := func(canvas, raw string) error {
		if raw == "" {
			return nil
		}
		var doc struct {
			Nodes    []mediaRootNode `json:"nodes"`
			Entities []struct {
				Key   string        `json:"key"`
				Value mediaRootNode `json:"value"`
			} `json:"entities"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return err
		}
		for _, item := range doc.Entities {
			if strings.HasPrefix(item.Key, "node:") {
				doc.Nodes = append(doc.Nodes, item.Value)
			}
		}
		for _, node := range doc.Nodes {
			v := node.Metadata.Version
			if node.ID != "" && v.Epoch != "" {
				roots[mediaVersionRoot{canvas, v.Epoch, node.ID, v.Generation}] = true
			}
		}
		return nil
	}
	query := r.db.Where("user_id = ?", owner)
	if canvasID != "" {
		query = query.Where("id = ?", canvasID)
	}
	var canvases []model.CanvasProject
	if err := query.Select("id", "payload_json").Find(&canvases).Error; err != nil {
		return nil, err
	}
	live := []string{}
	for _, canvas := range canvases {
		live = append(live, canvas.ID)
		if err := collect(canvas.ID, canvas.PayloadJSON); err != nil {
			return nil, err
		}
	}
	if r.db.Migrator().HasTable(&model.CanvasSnapshot{}) {
		query := r.db.Where("user_id = ?", owner)
		if canvasID != "" {
			query = query.Where("canvas_id = ?", canvasID)
		}
		var snapshots []model.CanvasSnapshot
		if err := query.Select("canvas_id", "payload_json").Find(&snapshots).Error; err != nil {
			return nil, err
		}
		for _, snapshot := range snapshots {
			if err := collect(snapshot.CanvasID, snapshot.PayloadJSON); err != nil {
				return nil, err
			}
		}
	}
	// Undo is a recoverable document reference, but a permanently deleted
	// canvas cannot be restored through its obsolete actor history.
	if len(live) > 0 && r.db.Migrator().HasTable(&model.CanvasActorHistory{}) && r.db.Migrator().HasTable(&model.CanvasDocumentEpoch{}) {
		var histories []model.CanvasActorHistory
		epochTable := r.db.NamingStrategy.TableName("CanvasDocumentEpoch")
		if err := r.db.Model(&model.CanvasActorHistory{}).Select("canvas_actor_histories.canvas_id", "forward_json", "inverse_json").Joins("JOIN "+epochTable+" AS current_epoch ON current_epoch.canvas_id = canvas_actor_histories.canvas_id AND current_epoch.epoch = canvas_actor_histories.epoch AND current_epoch.owner_id = canvas_actor_histories.owner_id").Where("canvas_actor_histories.owner_id = ? AND canvas_actor_histories.canvas_id IN ? AND state IN ?", owner, live, []string{"applied", "undone"}).Find(&histories).Error; err != nil {
			return nil, err
		}
		for _, history := range histories {
			for _, raw := range []string{history.ForwardJSON, history.InverseJSON} {
				if err := collect(history.CanvasID, raw); err != nil {
					return nil, err
				}
			}
		}
	}
	return roots, nil
}

// Caller holds the account/canvas write lock. Task-backed rows survive until
// the original task's existing audit-retention boundary has expired too.
func (r *Repository) PruneCanvasMediaVersions(owner, canvasID string) error {
	if !r.mediaVersionsEnabled {
		return nil
	}
	roots, err := r.canvasMediaVersionRoots(owner, canvasID)
	if err != nil {
		return err
	}
	query := r.db.Where("owner_id = ?", owner)
	if canvasID != "" {
		query = query.Where("canvas_id = ?", canvasID)
	}
	var rows []model.CanvasMediaVersion
	if err = query.Select("id", "canvas_id", "epoch", "node_id", "generation", "task_id", "source_version_id", "parent_version_id").Find(&rows).Error; err != nil {
		return err
	}
	tasks, err := r.mediaVersionTaskRoots(owner, rows)
	if err != nil {
		return err
	}
	retained := retainedMediaVersionIDs(rows, roots, tasks)
	ids := []string{}
	for _, row := range rows {
		if !retained[row.ID] {
			ids = append(ids, row.ID)
		}
	}
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		if err = r.db.Where("owner_id = ? AND id IN ?", owner, ids[start:end]).Delete(&model.CanvasMediaVersion{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) mediaVersionTaskRoots(owner string, rows []model.CanvasMediaVersion) (map[string]bool, error) {
	taskIDs := []string{}
	for _, row := range rows {
		if row.TaskID != nil {
			taskIDs = append(taskIDs, *row.TaskID)
		}
	}
	tasks := map[string]bool{}
	if len(taskIDs) > 0 && r.db.Migrator().HasTable(&model.Task{}) {
		for start := 0; start < len(taskIDs); start += 200 {
			var retained []string
			if err := r.db.Model(&model.Task{}).Where("user_id = ? AND id IN ?", owner, taskIDs[start:min(start+200, len(taskIDs))]).Pluck("id", &retained).Error; err != nil {
				return nil, err
			}
			for _, id := range retained {
				tasks[id] = true
			}
		}
	}
	return tasks, nil
}

func retainedMediaVersionIDs(rows []model.CanvasMediaVersion, roots map[mediaVersionRoot]bool, tasks map[string]bool) map[string]bool {
	retained := map[string]bool{}
	byID := map[string]model.CanvasMediaVersion{}
	queue := []string{}
	for _, row := range rows {
		byID[row.ID] = row
		if roots[mediaVersionRoot{row.CanvasID, row.Epoch, row.NodeID, row.Generation}] || row.TaskID != nil && tasks[*row.TaskID] {
			retained[row.ID] = true
			queue = append(queue, row.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		row := byID[id]
		for _, source := range []string{row.SourceVersionID, row.ParentVersionID} {
			if _, exists := byID[source]; exists && !retained[source] {
				retained[source] = true
				queue = append(queue, source)
			}
		}
	}
	return retained
}

func (r *Repository) CanvasMediaVersionOwners() ([]string, error) {
	owners := []string{}
	if !r.mediaVersionsEnabled {
		return owners, nil
	}
	err := r.db.Model(&model.CanvasMediaVersion{}).Distinct().Pluck("owner_id", &owners).Error
	return owners, err
}
