package tasks

import (
	"context"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"time"
)

type TaskRequest struct {
	Mode           string         `json:"mode"`
	Type           string         `json:"type,omitempty"`
	Operation      string         `json:"operation,omitempty"`
	Prompt         string         `json:"prompt"`
	LogicalModelID string         `json:"logicalModelId,omitempty"`
	Input          map[string]any `json:"input,omitempty"`
}
type Consumer struct {
	Kind         string `json:"kind,omitempty"`
	RowID        string `json:"rowId,omitempty"`
	ColumnID     string `json:"columnId,omitempty"`
	Field        string `json:"field,omitempty"`
	OutputNodeID string `json:"outputNodeId,omitempty"`
}
type SubmitRequest struct {
	Command      commands.Command `json:"command"`
	TargetNodeID string           `json:"targetNodeId"`
	Task         TaskRequest      `json:"task"`
	Consumer     Consumer         `json:"consumer,omitempty"`
}
type TaskHandle struct {
	TaskID  string           `json:"taskId"`
	Receipt commands.Receipt `json:"receipt"`
}
type PrevisSubmitRequest struct {
	Command commands.Command `json:"command"`
	SceneID string           `json:"sceneId"`
	ShotID  string           `json:"shotId"`
	Options map[string]any   `json:"options,omitempty"`
}
type PreparedTask struct {
	Task             model.Task
	BillingOrder     *model.BillingOrder
	ActiveTaskLimit  int
	ChannelID        string
	ChannelUpdatedAt time.Time
	ModelUpdatedAt   time.Time
}
type TaskSubmitter interface {
	Prepare(context.Context, identity.Principal, TaskRequest) (PreparedTask, error)
	Commit(context.Context, *repository.Repository, PreparedTask) error
	Wake()
}
