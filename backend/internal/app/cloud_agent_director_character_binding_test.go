package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func directorBindingPatchCall(t *testing.T, payload, id string, operation map[string]any) cloudAgentCall {
	t.Helper()
	doc := mustCreationDocument(t, payload)
	scene, ok := findPrevisScene(creationMaps(doc["previsScenes"]), "scene")
	if !ok {
		t.Fatal("fixture scene missing")
	}
	return directorTestCall(id, map[string]any{
		"action": "apply_patch", "sceneId": "scene", "expectedSceneHash": creationHash(scene),
		"patch": map[string]any{"operations": []any{operation}},
	})
}

// 角色绑定缺字段是模型可以自纠的参数错误：两种权限模式下都必须作为字段级回执交回模型，
// 不能在审批准入时把整轮判死。
func TestDirectorCharacterBindingMissingFieldIsCorrectable(t *testing.T) {
	for _, mode := range []string{"request_approval", "auto"} {
		t.Run(mode, func(t *testing.T) {
			payload := directorSceneDocument(t)
			call := directorBindingPatchCall(t, payload, "director-bind", map[string]any{
				"type": "object_add", "id": "hero-actor", "kind": "actor", "primitive": "character", "characterAssetId": "character-asset",
			})
			s, run, state := directorFixture(t, mode, payload, call)
			if err := s.executeCloudAgentToolCall(run, state); err != nil {
				t.Fatal(err)
			}
			run, state = reloadPiRun(t, s, run.ID)
			if run.Status == "failed" || state.Approval != nil || state.CallIndex != 1 {
				t.Fatalf("missing binding field must be a tool receipt: status=%s failure=%q approval=%v index=%d", run.Status, run.FailureMessage, state.Approval != nil, state.CallIndex)
			}
			result := directorToolMessage(t, state, "director-bind")
			if result["field"] != "characterVersionId" || result["issue"] != "required" || result["errorClass"] != cloudAgentToolErrorSchemaError || result["requiredAction"] != "fix_arguments" {
				t.Fatalf("receipt must name the missing field: %v", result)
			}
			if message, _ := result["error"].(string); !strings.Contains(message, "character.versionId") || !strings.Contains(message, "canvas_get_state") {
				t.Fatalf("receipt must explain where the value comes from: %v", result["error"])
			}
		})
	}
}

// 按 canvas_get_state 返回的 character 原样填写三个字段必须通过；current 策略下读取视图
// 返回资产当前主版本，校验口径必须与之一致，但不能放宽到任意版本。
func TestDirectorCharacterBindingAcceptsReadView(t *testing.T) {
	s, db, run := piAgentTestFixture(t)
	node := seedCloudAgentCharacter(t, db, run.CanvasID)
	scene, err := cloudAgentPrevisSceneCreateTemplate(cloudAgentPrevisSceneCreateArgs{SceneID: "scene", Title: "客厅", TemplateID: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"nodes": []any{node}, "connections": []any{}, "previsScenes": []any{scene}})
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", run.CanvasID).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	doc := mustCreationDocument(t, string(raw))
	view, err := cloudAgentCanvasState(s.repo, "user", run.CanvasID, doc, 0, []string{"character-card"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	read := view.(map[string]any)["nodes"].([]any)[0].(map[string]any)
	character := read["character"].(map[string]any)
	if character["versionPolicy"] != "current" || character["versionId"] == "" {
		t.Fatalf("fixture must read a current-policy character: %v", character)
	}
	bind := func(versionID string) error {
		call := directorBindingPatchCall(t, string(raw), "director-bind", map[string]any{
			"type": "object_add", "id": "hero-actor", "kind": "actor", "primitive": "character",
			"characterAssetId": character["assetId"], "characterVersionId": versionID, "referenceNodeId": read["id"],
		})
		_, err := prepareCloudAgentDirectorMutation(s.repo, "user", run.CanvasID, run.ID, call)
		return err
	}
	if err := bind(stringValue(character["versionId"])); err != nil {
		t.Fatalf("binding copied from canvas_get_state was rejected: %v", err)
	}
	err = bind("character-v1")
	var fieldErr *cloudAgentFieldArgumentError
	if !errors.As(err, &fieldErr) || fieldErr.Field != "characterVersionId" || fieldErr.Issue != "mismatch" {
		t.Fatalf("a version other than the read view must stay rejected as a correctable mismatch: %v", err)
	}
}
