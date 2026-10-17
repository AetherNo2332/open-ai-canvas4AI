package tasks

import (
	"context"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"time"
)

type MaterializationStatus struct {
	TaskID        string     `json:"taskId"`
	State         string     `json:"state"`
	Outcome       string     `json:"outcome,omitempty"`
	Message       string     `json:"message,omitempty"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
}

func (s *Service) ReadMaterialization(ctx context.Context, p identity.Principal, id string) (MaterializationStatus, error) {
	b, err := s.repo.CanvasTaskBinding(id)
	if err != nil || b.OwnerID != p.OwnerID {
		return MaterializationStatus{}, &commands.Error{Reason: "entity_deleted", Message: "任务绑定不存在"}
	}
	if _, err = s.commands.ReadSnapshot(ctx, p, b.CanvasID); err != nil {
		return MaterializationStatus{}, err
	}
	j, err := s.repo.CanvasMaterialization(id)
	if s.repo.IsMissing(err) {
		return MaterializationStatus{TaskID: id, State: "waiting"}, nil
	}
	if err != nil {
		return MaterializationStatus{}, err
	}
	return MaterializationStatus{TaskID: id, State: j.State, Outcome: j.Outcome, Message: j.LastError, Attempts: j.Attempts, NextAttemptAt: j.NextAttemptAt}, nil
}
func (s *Service) RetryMaterialization(ctx context.Context, p identity.Principal, id string) error {
	if p.Role == "viewer" || !p.HasScope("canvas.write") {
		return &commands.Error{Reason: "permission_denied", Message: "没有写回权限"}
	}
	if _, err := s.ReadMaterialization(ctx, p, id); err != nil {
		return err
	}
	err := s.Materialize(ctx, id)
	if err != nil && ctx.Err() == nil {
		_ = s.repo.RecordCanvasMaterializationFailure(id)
	}
	return err
}
