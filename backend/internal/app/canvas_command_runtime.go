package app

import (
	"errors"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// CanvasCommandServices lazily enables the durable canvas command/task chain
// used by the web canvas. Command admission, rendering and result writeback
// share the authenticated server session and permission checks.
func (s *Service) CanvasCommandServices() (*commands.Service, *tasks.Service) {
	if s.canvasCommands != nil && s.canvasTasks != nil {
		return s.canvasCommands, s.canvasTasks
	}
	s.canvasCommandMode = true
	s.canvasCommands = commands.NewService(s.repo, commands.ServiceOptions{Validation: s.CanvasCommandValidation})
	s.canvasTasks = tasks.NewService(s.repo, s.canvasCommands, s.CanvasTaskSubmitter(), s.CanvasTaskOptions())
	return s.canvasCommands, s.canvasTasks
}

// Agent previews keep their existing lease/writeback path. Web canvas tasks
// use durable bindings so activating commands never changes Agent behavior.
func (s *Service) canvasTaskOwnsWriteback(task *model.Task) (bool, error) {
	if !s.canvasCommandMode || task.AgentRunID != "" {
		return false, nil
	}
	_, err := s.repo.CanvasTaskBinding(task.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}
