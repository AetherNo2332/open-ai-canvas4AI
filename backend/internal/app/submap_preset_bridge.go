package app

import (
	"yingce/backend/internal/canvas"
	"yingce/backend/internal/model"
)

type UserSubmapPresetRequest = canvas.UserSubmapPresetRequest

func (s *Service) ListUserSubmapPresets(userID string) ([]model.UserSubmapPreset, error) {
	return s.canvasDomain().ListUserSubmapPresets(userID)
}

func (s *Service) CreateUserSubmapPreset(userID string, req UserSubmapPresetRequest) (model.UserSubmapPreset, error) {
	return s.canvasDomain().CreateUserSubmapPreset(userID, req)
}

func (s *Service) UpdateUserSubmapPreset(userID, id string, req UserSubmapPresetRequest) (model.UserSubmapPreset, error) {
	return s.canvasDomain().UpdateUserSubmapPreset(userID, id, req)
}

func (s *Service) DeleteUserSubmapPreset(userID, id string) error {
	return s.canvasDomain().DeleteUserSubmapPreset(userID, id)
}
