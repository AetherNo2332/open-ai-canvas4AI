package previs

import (
	"math"
	"strings"
	"testing"
)

func TestSceneUpdateRules(t *testing.T) {
	title, color, active := "场景", "#AAbbCC", "shot:1"
	intensity := 2.0
	grid := false
	scene := map[string]any{"id": "scene", "title": "before"}
	result, err := ApplySceneUpdate(scene, SceneUpdate{ID: "scene", Title: &title, Background: &color, EnvironmentIntensity: &intensity, GridVisible: &grid, ActiveShotID: &active}, AgentIDs)
	if err != nil || result.ID != "scene" || scene["title"] != title || scene["gridVisible"] != false {
		t.Fatalf("%+v %v %v", result, err, scene)
	}
	for _, value := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		if _, err := ApplySceneUpdate(scene, SceneUpdate{ID: "scene", EnvironmentIntensity: &value}, AgentIDs); err == nil {
			t.Fatal("invalid intensity accepted")
		}
	}
	for _, value := range []string{"", " name", "name\n", strings.Repeat("字", 161)} {
		if _, err := ApplySceneUpdate(scene, SceneUpdate{ID: "scene", Title: &value}, AgentIDs); err == nil {
			t.Fatal("invalid title accepted")
		}
	}
	for _, value := range []string{"red", "#12345", "#zzzzzz"} {
		if _, err := ApplySceneUpdate(scene, SceneUpdate{ID: "scene", Background: &value}, AgentIDs); err == nil {
			t.Fatal("invalid color accepted")
		}
	}
}

func TestSceneUpdatePreservesIDPolicyDifference(t *testing.T) {
	id := "镜头"
	scene := map[string]any{"id": "scene", "title": "before"}
	if _, err := ApplySceneUpdate(scene, SceneUpdate{ID: "scene", ActiveShotID: &id}, AgentIDs); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySceneUpdate(scene, SceneUpdate{ID: "scene", ActiveShotID: &id}, CommandIDs); err == nil {
		t.Fatal("command accepted non-ASCII ID")
	}
	if _, err := ApplySceneUpdate(scene, SceneUpdate{ID: "other"}, AgentIDs); err == nil {
		t.Fatal("scene identity mismatch accepted")
	}
}
