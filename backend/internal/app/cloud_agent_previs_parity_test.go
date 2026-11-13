package app

import (
	"encoding/json"
	"reflect"
	"testing"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/identity"
)

func TestPrevisSceneUpdateParity(t *testing.T) {
	title, color, active := "新场景", "#aabbcc", "scene-shot-1"
	intensity := 1.5
	grid := false
	scene, err := cloudAgentPrevisSceneCreateTemplate(cloudAgentPrevisSceneCreateArgs{SceneID: "scene", Title: "原场景", TemplateID: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"nodes": []any{}, "connections": []any{}, "previsScenes": []any{scene}})
	var doc commands.Document
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	op := cloudAgentPrevisPatchOperation{Type: "scene_update", ID: "scene", Title: &title, Background: &color, EnvironmentIntensity: &intensity, GridVisible: &grid, ActiveShotID: &active}
	item, err := cloudAgentPrevisApplyPatchOperation(scene, op, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx := commands.ValidationContext{Principal: identity.Principal{ID: "owner", OwnerID: "owner", Role: "owner", Scopes: identity.OwnerScopes()}}
	req := commands.Command{Protocol: 1, CanvasID: "canvas", DocumentEpoch: "epoch", OperationID: "op", ClientID: "client", Operations: []commands.Operation{{Type: "edit_previs_scene", ID: "scene", PrevisOperations: []commands.PrevisOperation{{Type: op.Type, ID: op.ID, Title: op.Title, Background: op.Background, EnvironmentIntensity: op.EnvironmentIntensity, GridVisible: op.GridVisible, ActiveShotID: op.ActiveShotID}}}}}
	result, err := commands.Plan(doc, req, ctx)
	if err != nil {
		t.Fatal(err)
	}
	native := creationMaps(result.Document["previsScenes"])[0]
	for _, key := range []string{"title", "background", "environmentIntensity", "gridVisible", "activeShotId"} {
		if !reflect.DeepEqual(scene[key], native[key]) {
			t.Fatalf("%s diverged: %v / %v", key, scene[key], native[key])
		}
	}
	if item.Summary != "修改场景级设置" || item.NodeTitle != title {
		t.Fatalf("approval summary changed: %+v", item)
	}
}
