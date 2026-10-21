package app

import (
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
)

// CanvasCommandServices lazily enables the durable canvas command/task chain
// used by the web canvas so previs submissions and materialization use one
// permissioned path.
func (s *Service) CanvasCommandServices() (*commands.Service, *tasks.Service) {
	if s.canvasCommands != nil && s.canvasTasks != nil {
		return s.canvasCommands, s.canvasTasks
	}
	s.canvasCommandMode = true
	s.canvasCommands = commands.NewService(s.repo, commands.ServiceOptions{Validation: s.CanvasCommandValidation})
	s.canvasTasks = tasks.NewService(s.repo, s.canvasCommands, s.CanvasTaskSubmitter(), s.CanvasTaskOptions())
	return s.canvasCommands, s.canvasTasks
}
