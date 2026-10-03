package repository

import (
	"infinite-canvas/backend/internal/model"
	"time"
)

type ObservabilityData struct {
	Runs      []model.CloudAgentExecution
	Tasks     []model.Task
	Logs      []model.ApiCallLog
	Events    []model.CloudAgentEventRecord
	Workers   []model.AgentRuntimeInstance
	Truncated bool
}

func (r *Repository) ObservabilityData(from time.Time) (ObservabilityData, error) {
	var data ObservabilityData
	const limit = 10000
	// Only committed rows and selected metadata are read. No transcript, request
	// body, prompt, credential or upstream URL is loaded for aggregation.
	queries := []struct {
		dst    any
		table  any
		fields string
		where  string
		order  string
	}{
		{&data.Runs, &model.CloudAgentExecution{}, "id,status,engine,runtime_phase,conversation_id,parent_id,active_task_id,created_at,updated_at", "created_at >= ? OR status IN ('running','queued','waiting_approval')", "created_at DESC"},
		{&data.Tasks, &model.Task{}, "id,agent_run_id,trace_id,status,operation,model,attempts,created_at,started_at,completed_at,updated_at", "created_at >= ? OR status IN ('running','queued')", "created_at DESC"},
		{&data.Logs, &model.ApiCallLog{}, "id,task_id,trace_id,capability,status,model,input_tokens,output_tokens,cached_tokens,duration_ms,started_at,created_at,billing_order_id", "created_at >= ?", "created_at DESC"},
		{&data.Events, &model.CloudAgentEventRecord{}, "run_id,sequence,event_json,created_at", "created_at >= ?", "created_at DESC"},
	}
	for _, q := range queries {
		if err := r.db.Model(q.table).Select(q.fields).Where(q.where, from).Order(q.order).Limit(limit + 1).Find(q.dst).Error; err != nil {
			return data, err
		}
	}
	if r.db.Migrator().HasTable(&model.AgentRuntimeInstance{}) {
		if err := r.db.Select("id,active,capacity,draining,updated_at").Where("updated_at > ?", time.Now().Add(-45*time.Second)).Find(&data.Workers).Error; err != nil {
			return data, err
		}
	}
	data.Truncated = len(data.Runs) > limit || len(data.Tasks) > limit || len(data.Logs) > limit || len(data.Events) > limit
	return data, nil
}
