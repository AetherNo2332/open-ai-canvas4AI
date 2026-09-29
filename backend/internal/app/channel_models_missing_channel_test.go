package app

import (
	"errors"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestAdminChannelModelsReturnsNotFoundForMissingChannel(t *testing.T) {
	svc, _ := newChannelModelTestService(t)
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}

	_, err := svc.AdminChannelModels(admin, "missing-channel")
	if err == nil {
		t.Fatal("expected missing channel error")
	}
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected structured application error, got %T: %v", err, err)
	}
	if appErr.Status != 404 {
		t.Fatalf("status = %d, want 404 (error: %v)", appErr.Status, err)
	}
}
