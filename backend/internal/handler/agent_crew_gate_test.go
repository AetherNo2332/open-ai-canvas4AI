package handler

import (
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestCrewRoutesReturnStableFeatureDisabledWhenGateOff(t *testing.T) {
	call, svc, db := workspaceHTTPFixture(t)
	if err := db.Where("key = ?", "feature_availability").Delete(&model.SystemSetting{}).Error; err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "own/crews", "", true); w.Code != 403 {
		t.Fatalf("gate status %d %s", w.Code, w.Body)
	}
	if svc == nil {
		t.Fatal("service missing")
	}
}
