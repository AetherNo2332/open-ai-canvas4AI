package repository

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
	"strings"
	"time"
)

var ErrToolRuntimeUnavailable = errors.New("worker tool runtime unavailable for admission")

func (r *Repository) WithToolRuntimeIdentity(instance, snapshot string) *Repository {
	return &Repository{db: r.db, toolRuntimeInstanceID: instance, workerToolSnapshotID: snapshot}
}

func (r *Repository) ToolSnapshots() ([]model.AgentToolSnapshot, error) {
	rows := []model.AgentToolSnapshot{}
	err := r.db.Omit("contents").Order("created_at DESC,id").Limit(500).Find(&rows).Error
	return rows, err
}

type ToolSnapshotListQuery struct {
	Page     int
	PageSize int
	Source   string
	Tool     string
	Version  string
	RunID    string
}

func (r *Repository) ListToolSnapshots(p ToolSnapshotListQuery) ([]model.AgentToolSnapshot, int64, error) {
	q := r.db.Model(&model.AgentToolSnapshot{})
	if p.Source != "" {
		q = q.Where("source = ?", p.Source)
	}
	if p.RunID != "" {
		q = q.Where("source = ? AND instance_id = ?", "task", p.RunID)
	}
	escape := func(s string) string { return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s) }
	if p.Tool != "" {
		name, _ := json.Marshal(p.Tool)
		q = q.Where("tool_names LIKE ? ESCAPE '!'", "%"+escape(string(name))+"%")
	}
	if p.Version != "" {
		pattern := "%" + escape(p.Version) + "%"
		q = q.Where("build_commit LIKE ? ESCAPE '!' OR schema_version LIKE ? ESCAPE '!'", pattern, pattern)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.AgentToolSnapshot{}
	err := q.Order("created_at DESC,id").Offset((p.Page - 1) * p.PageSize).Limit(p.PageSize).Find(&rows).Error
	return rows, total, err
}
func (r *Repository) ToolSnapshotRun(id string) (*model.CloudAgentExecution, error) {
	var row model.CloudAgentExecution
	err := r.db.Select("id", "state_json").Where("id = ? AND engine = ?", id, "pi").First(&row).Error
	return &row, err
}
func (r *Repository) ToolSnapshot(id string) (*model.AgentToolSnapshot, error) {
	var row model.AgentToolSnapshot
	err := r.db.First(&row, "id = ?", id).Error
	if err == nil {
		var body model.AgentToolSnapshotContent
		err = r.db.First(&body, "id = ?", row.ContentID).Error
		row.Contents = body.Contents
	}
	return &row, err
}

func (r *Repository) FrozenToolSnapshot(runID string) (*model.AgentToolSnapshot, error) {
	var row model.AgentToolSnapshot
	err := r.db.Omit("contents").Where("source = ? AND instance_id = ?", "task", runID).First(&row).Error
	return &row, err
}
func (r *Repository) ToolSnapshotCount() (int64, error) {
	var count int64
	err := r.db.Model(&model.AgentToolSnapshot{}).Count(&count).Error
	return count, err
}
func (r *Repository) SaveToolSnapshot(row *model.AgentToolSnapshot) error {
	row.ToolNames = []string{}
	var artifact struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal([]byte(row.Contents), &artifact)
	for _, tool := range artifact.Tools {
		row.ToolNames = append(row.ToolNames, tool.Function.Name)
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		counter := model.AgentToolSnapshotCounter{ID: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
			return err
		}
		if err := tx.Model(&counter).Where("id = ?", 1).UpdateColumn("value", gorm.Expr("value + 0")).Error; err != nil {
			return err
		}
		var found model.AgentToolSnapshot
		var body model.AgentToolSnapshotContent
		bodyErr := tx.Where("contents = ?", row.Contents).First(&body).Error
		if errors.Is(bodyErr, gorm.ErrRecordNotFound) {
			body = model.AgentToolSnapshotContent{ID: uuid.NewString(), Contents: row.Contents}
			if err := tx.Create(&body).Error; err != nil {
				return err
			}
		} else if bodyErr != nil {
			return bodyErr
		}
		row.ContentID = body.ID
		err := tx.Where("source = ? AND instance_id = ? AND build_commit = ? AND content_id = ?", row.Source, row.InstanceID, row.BuildCommit, row.ContentID).First(&found).Error
		if err == nil {
			found.Contents = row.Contents
			*row = found
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(row).Error
	})
}

func (r *Repository) UpdateToolSnapshotAlerts(backend string, active []model.AgentToolSnapshotAlert) ([]model.AgentToolSnapshotAlert, error) {
	rows := []model.AgentToolSnapshotAlert{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		keep := []string{}
		for _, a := range active {
			var old model.AgentToolSnapshotAlert
			err := tx.Where("backend_snapshot_id = ? AND worker_snapshot_id = ? AND instance_id = ? AND code = ? AND resolved_at IS NULL", backend, a.WorkerSnapshotID, a.InstanceID, a.Code).First(&old).Error
			if err == nil {
				a = old
				if err = tx.Model(&old).Update("last_seen_at", now).Error; err != nil {
					return err
				}
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				a.BackendSnapshotID = backend
				a.FirstSeenAt = now
				a.LastSeenAt = now
				if err = tx.Create(&a).Error; err != nil {
					return err
				}
			} else {
				return err
			}
			keep = append(keep, a.ID)
		}
		q := tx.Model(&model.AgentToolSnapshotAlert{}).Where("backend_snapshot_id = ? AND resolved_at IS NULL", backend)
		if len(keep) > 0 {
			q = q.Where("id NOT IN ?", keep)
		}
		if err := q.Update("resolved_at", now).Error; err != nil {
			return err
		}
		return tx.Order("first_seen_at DESC").Limit(200).Find(&rows).Error
	})
	return rows, err
}
func (r *Repository) ToolRuntimes() ([]model.AgentToolRuntime, error) {
	rows := []model.AgentToolRuntime{}
	err := r.db.Order("instance_id").Find(&rows).Error
	return rows, err
}
func (r *Repository) ToolRuntime(id string) (*model.AgentToolRuntime, error) {
	var row model.AgentToolRuntime
	err := r.db.First(&row, "instance_id = ?", id).Error
	return &row, err
}
func (r *Repository) RegisterToolRuntime(row *model.AgentToolRuntime) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		counter := model.AgentAdmissionCounter{ID: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
			return err
		}
		if err := tx.Model(&counter).Where("id = ?", 1).UpdateColumn("value", gorm.Expr("value + 0")).Error; err != nil {
			return err
		}
		var old model.AgentToolRuntime
		err := tx.First(&old, "instance_id = ?", row.InstanceID).Error
		if err == nil {
			if old.BootID == row.BootID && (row.LoadGeneration < old.LoadGeneration || row.LoadGeneration == old.LoadGeneration && row.SnapshotID != old.SnapshotID) {
				return ErrCreationConflict
			}
			if old.BootID != row.BootID && old.LastHeartbeatAt.After(time.Now().Add(-45*time.Second)) {
				return ErrCreationConflict
			}
			row.Revision = old.Revision
			row.ManualDraining = old.ManualDraining
			return tx.Model(&old).Updates(map[string]any{"boot_id": row.BootID, "snapshot_id": row.SnapshotID, "load_generation": row.LoadGeneration, "load_error": row.LoadError, "last_heartbeat_at": row.LastHeartbeatAt}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row.Revision = 1
		return tx.Create(row).Error
	})
}
func (r *Repository) HeartbeatToolRuntime(instance, boot, snapshot string, generation int64, fileState string) error {
	query := r.db.Model(&model.AgentToolRuntime{}).Where("instance_id = ? AND boot_id = ? AND snapshot_id = ?", instance, boot, snapshot)
	query = query.Where("load_generation = ?", generation)
	updates := map[string]any{"last_heartbeat_at": time.Now()}
	if fileState != "" {
		updates["artifact_file_state"] = fileState
	}
	res := query.Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}
func (r *Repository) DrainToolRuntime(id string, revision int64, draining bool, audit *model.AdminAuditEvent) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Claims lock this row before granting any lease.
		res := tx.Model(&model.AgentToolRuntime{}).Where("instance_id = ? AND revision = ?", id, revision).Updates(map[string]any{"manual_draining": draining, "revision": gorm.Expr("revision + 1")})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrCreationConflict
		}
		return tx.Create(audit).Error
	})
}
func (r *Repository) LockToolRuntime(id string) (*model.AgentToolRuntime, error) {
	if err := r.db.Model(&model.AgentToolRuntime{}).Where("instance_id = ?", id).UpdateColumn("revision", gorm.Expr("revision + 0")).Error; err != nil {
		return nil, err
	}
	return r.ToolRuntime(id)
}
func (r *Repository) ActiveToolRuns() ([]model.CloudAgentExecution, error) {
	rows := []model.CloudAgentExecution{}
	err := r.db.Select("id", "state_json", "lease_owner", "lease_expires_at", "tool_runtime_instance_id", "tool_snapshot_id", "worker_tool_snapshot_id", "status", "wait_kind", "revision").Where("engine = ? AND status IN ?", "pi", []string{"queued", "running", "waiting_approval"}).Order("created_at,id").Limit(501).Find(&rows).Error
	return rows, err
}

func (r *Repository) BindToolRunIdentity(id, instance, snapshot, workerSnapshot string) error {
	return r.db.Model(&model.CloudAgentExecution{}).Where("id = ?", id).Updates(map[string]any{"tool_runtime_instance_id": instance, "tool_snapshot_id": snapshot, "worker_tool_snapshot_id": workerSnapshot}).Error
}
