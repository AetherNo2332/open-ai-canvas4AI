package database

import (
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"reflect"
	"sync"
)

var agentCallbackMu sync.Mutex

// Register only on migrated databases. Small repository fixtures which do not
// exercise the scheduler do not need its tables; production schema checks do.
func InstallAgentEventCallbacks(db *gorm.DB) {
	if db == nil {
		return
	}
	agentCallbackMu.Lock()
	defer agentCallbackMu.Unlock()
	if db.Callback().Update().Get("canvas:agent_event_before") != nil || !db.Migrator().HasTable(&model.AgentWakeEvent{}) {
		return
	}
	_ = db.Callback().Update().Before("gorm:update").Register("canvas:agent_event_before", func(tx *gorm.DB) {
		table := tx.Statement.Table
		if table != "tasks" && table != "cloud_agent_executions" {
			return
		}
		if changes, ok := tx.Statement.Dest.(map[string]interface{}); ok {
			relevant := false
			for _, key := range []string{"status", "state_json", "runtime_phase", "wait_reason"} {
				if _, found := changes[key]; found {
					relevant = true
				}
			}
			if !relevant {
				return
			}
		}
		query := tx.Session(&gorm.Session{NewDB: true}).Table(table)
		constrained := false
		if where, ok := tx.Statement.Clauses["WHERE"]; ok {
			query = query.Clauses(where.Expression)
			constrained = true
		}
		if tx.Statement.Schema != nil {
			if field := tx.Statement.Schema.LookUpField("ID"); field != nil && tx.Statement.ReflectValue.IsValid() && tx.Statement.ReflectValue.Kind() == reflect.Struct {
				value, zero := field.ValueOf(tx.Statement.Context, tx.Statement.ReflectValue)
				if !zero {
					query = query.Where("id = ?", value)
					constrained = true
				}
			}
		}
		if !constrained {
			return
		}
		var ids []string
		if err := query.Pluck("id", &ids).Error; err != nil {
			tx.AddError(err)
			return
		}
		tx.Statement.Settings.Store("canvas:agent_event_ids", ids)
	})
	_ = db.Callback().Update().After("gorm:update").Before("gorm:commit_or_rollback_transaction").Register("canvas:agent_event_after", func(tx *gorm.DB) {
		if tx.Error != nil || tx.RowsAffected == 0 {
			return
		}
		raw, found := tx.Statement.Settings.Load("canvas:agent_event_ids")
		if !found {
			return
		}
		tx.Statement.Settings.Delete("canvas:agent_event_ids")
		clean := tx.Session(&gorm.Session{NewDB: true})
		for _, id := range raw.([]string) {
			event := model.AgentWakeEvent{Kind: "task_changed", TaskID: id}
			if tx.Statement.Table == "tasks" {
				var task model.Task
				if err := clean.Select("agent_run_id", "user_id").First(&task, "id = ?", id).Error; err != nil {
					tx.AddError(err)
					return
				}
				if task.AgentRunID != "" {
					var run model.CloudAgentExecution
					if err := clean.Select("id", "user_id", "revision", "engine").Where("id = ? AND user_id = ?", task.AgentRunID, task.UserID).Find(&run).Error; err != nil {
						tx.AddError(err)
						return
					}
					if run.ID != "" && run.Engine == "pi" {
						event.RunID = run.ID
						event.UserID = run.UserID
						event.Revision = run.Revision
					}
				}
			}
			if tx.Statement.Table == "cloud_agent_executions" {
				var run model.CloudAgentExecution
				if err := clean.Select("id", "user_id", "revision", "engine").First(&run, "id = ?", id).Error; err != nil {
					tx.AddError(err)
					return
				}
				if run.Engine != "pi" {
					continue
				}
				event = model.AgentWakeEvent{Kind: "run_changed", RunID: id, UserID: run.UserID, Revision: run.Revision}
			}
			if err := model.AppendAgentWake(clean, event); err != nil {
				tx.AddError(err)
				return
			}
		}
	})
	_ = db.Callback().Create().After("gorm:create").Before("gorm:commit_or_rollback_transaction").Register("canvas:agent_event_create", func(tx *gorm.DB) {
		if tx.Error != nil || tx.RowsAffected == 0 || tx.Statement.Table != "cloud_agent_executions" {
			return
		}
		run, ok := tx.Statement.Dest.(*model.CloudAgentExecution)
		if !ok || run.Engine != "pi" {
			return
		}
		if err := model.AppendAgentWake(tx.Session(&gorm.Session{NewDB: true}), model.AgentWakeEvent{RunID: run.ID, UserID: run.UserID, Revision: run.Revision, Kind: "run_changed"}); err != nil {
			tx.AddError(err)
		}
	})
}
