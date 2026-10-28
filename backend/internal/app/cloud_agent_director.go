package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/repository"
)

var errDirectorWorkflowOrder = errors.New("image_to_director workflow order is invalid")

// cloudAgentDirectorUnderlyingCall compiles high-level director language into
// the existing checked previs contracts. The model never receives raw
// DirectorDesk project JSON or a second MCP surface.
func cloudAgentDirectorUnderlyingCall(call cloudAgentCall) (cloudAgentCall, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &raw); err != nil {
		return call, cloudAgentJSONArgumentError(err)
	}
	name := call.Function.Name
	underlying := call
	var target string
	var args map[string]any
	switch name {
	case "director_block_scene":
		target = "previs_scene_create"
		args = map[string]any{"canvasSnapshotHash": raw["canvasSnapshotHash"], "sceneId": raw["sceneId"], "title": raw["title"], "templateId": raw["templateId"]}
	case "director_design_shot":
		target = "previs_apply_patch"
		args = map[string]any{"snapshotHash": raw["snapshotHash"], "sceneId": raw["sceneId"], "operations": []any{map[string]any{"type": "shot_update", "id": raw["shotId"], "shotSize": raw["shotSize"], "cameraMove": raw["cameraMove"], "prompt": raw["prompt"]}}}
	case "director_draw_action":
		target = "previs_apply_patch"
		args = map[string]any{"snapshotHash": raw["snapshotHash"], "sceneId": raw["sceneId"], "operations": []any{map[string]any{"type": "object_motion_path_set", "id": raw["objectId"], "pathPoints": raw["pathPoints"], "pathSpeed": raw["pathSpeed"], "startDelay": raw["startDelay"], "orientToPath": raw["orientToPath"]}}}
	case "director_preview_shot":
		target = "previs_preview"
		args = map[string]any{"sceneId": raw["sceneId"], "shotId": raw["shotId"], "duration": raw["duration"], "fps": raw["fps"], "output": "clay_video"}
	default:
		return call, fmt.Errorf("director_tool_not_compilable:%s", name)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return call, err
	}
	underlying.Function.Name = target
	underlying.Function.Arguments = string(encoded)
	return underlying, nil
}

func cloudAgentDirectorTool(name string) bool {
	switch name {
	case "director_block_scene", "director_design_shot", "director_draw_action", "director_check_continuity", "director_preview_shot", "director_handoff_shot":
		return true
	default:
		return false
	}
}

func cloudAgentDirectorWriteTool(name string) bool {
	switch name {
	case "director_block_scene", "director_design_shot", "director_draw_action", "director_preview_shot":
		return true
	default:
		return false
	}
}

// cloudAgentDirectorReceipt adds one stable envelope to every successful
// high-level director result. requestId identifies the durable Agent run; the
// revision is the scene snapshot token returned by the checked previs layer.
// Keeping this envelope here avoids exposing a second DirectorDesk persistence
// contract while giving the UI and Harness a uniform correlation point.
func cloudAgentDirectorReceipt(runID, callID string, result any) any {
	fields, ok := result.(map[string]any)
	if !ok || fields == nil {
		return map[string]any{"requestId": runID, "callId": callID, "result": result}
	}
	out := make(map[string]any, len(fields)+4)
	for key, value := range fields {
		out[key] = value
	}
	out["requestId"] = runID
	out["callId"] = callID
	if _, exists := out["revision"]; !exists {
		if snapshot, exists := out["snapshotHash"].(string); exists && snapshot != "" {
			out["revision"] = snapshot
		} else if nested, ok := out["result"].(map[string]any); ok {
			if snapshot, exists := nested["snapshotHash"].(string); exists && snapshot != "" {
				out["revision"] = snapshot
			}
		}
	}
	return out
}

// Image-to-director is a server-checked workflow, not only a prompt convention.
// Read-only canvas calls may happen between phases, but scene writes cannot skip
// the real image receipt or the ordered blocking/design/continuity steps.
func cloudAgentDirectorWorkflowError(state *cloudAgentRuntime, call cloudAgentCall) error {
	if state == nil || state.Request.Workflow != "image_to_director" {
		return nil
	}
	switch call.Function.Name {
	case "director_block_scene":
		if state.DirectorWorkflowPhase != "inspected" {
			return fmt.Errorf("%w: director_block_scene 之前必须成功完成 canvas_inspect_image", errDirectorWorkflowOrder)
		}
	case "director_design_shot":
		if state.DirectorWorkflowPhase != "blocked" {
			return fmt.Errorf("%w: director_design_shot 之前必须成功完成 director_block_scene", errDirectorWorkflowOrder)
		}
	case "director_check_continuity":
		if state.DirectorWorkflowPhase != "designed" {
			return fmt.Errorf("%w: director_check_continuity 之前必须成功完成 director_design_shot", errDirectorWorkflowOrder)
		}
	case "director_preview_shot", "director_handoff_shot":
		if state.DirectorWorkflowPhase != "checked" {
			return fmt.Errorf("%w: 预览或交付之前必须成功完成 director_check_continuity", errDirectorWorkflowOrder)
		}
	}
	return nil
}

func cloudAgentAdvanceDirectorWorkflow(state *cloudAgentRuntime, toolName, callID string, result any, err error) {
	if state == nil || err != nil || state.Request.Workflow != "image_to_director" {
		return
	}
	switch toolName {
	case "canvas_inspect_image":
		state.DirectorWorkflowPhase = "inspected"
	case "director_block_scene":
		state.DirectorWorkflowPhase = "blocked"
		state.DirectorWorkflowMutationCallID = callID
	case "director_design_shot":
		state.DirectorWorkflowPhase = "designed"
	case "director_check_continuity":
		if directorContinuityPassed(result) {
			state.DirectorWorkflowPhase = "checked"
		}
	}
}

// directorContinuityPassed reports whether a continuity result cleared every
// blocking issue. It reads the receipt-wrapped director result and is fail-
// closed: a missing status, or a non-bool ok, counts as a non-pass, so a failed
// check never advances the image-to-director workflow to checked.
func directorContinuityPassed(result any) bool {
	fields, ok := result.(map[string]any)
	if !ok {
		return false
	}
	status, ok := fields["status"].(map[string]any)
	if !ok {
		return false
	}
	passed, _ := status["ok"].(bool)
	return passed
}

func directorWorkflowRollbackEligible(state *cloudAgentRuntime) bool {
	if state == nil || state.Request.Workflow != "image_to_director" {
		return false
	}
	return state.DirectorWorkflowPhase == "blocked" || state.DirectorWorkflowPhase == "designed"
}

// rollbackDirectorWorkflowDraft restores only an untouched draft mutation. A
// later canvas edit, including a user edit, changes the current snapshot hash
// and deliberately prevents silent overwrite.
func rollbackDirectorWorkflowDraft(repo *repository.Repository, userID string, state *cloudAgentRuntime, policy RuntimePolicySetting) error {
	if !directorWorkflowRollbackEligible(state) {
		return nil
	}
	mutation, err := repo.LatestCloudAgentCanvasMutation(userID, state.RuntimeRunID)
	if err != nil || mutation == nil || mutation.CanvasID != state.Request.CanvasID || mutation.StepID != state.DirectorWorkflowMutationCallID || mutation.BeforeJSON == "" || mutation.AfterSnapshotHash == "" {
		return nil
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if err != nil {
		return err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil || creationHash(doc) != mutation.AfterSnapshotHash {
		return nil
	}
	before, err := creationDocument(mutation.BeforeJSON)
	if err != nil {
		return err
	}
	if err := saveCloudAgentDocument(repo, canvas, before, policy); err != nil {
		return err
	}
	if err := repo.MarkCloudAgentCanvasMutationUndone(userID, state.RuntimeRunID, mutation.ID, time.Now().UTC()); err != nil {
		return err
	}
	state.DirectorWorkflowPhase = "rolled_back"
	return nil
}

func cloudAgentDirectorToolSchema(name string) map[string]any {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	vec := map[string]any{"type": "array", "minItems": 3, "maxItems": 3, "items": map[string]any{"type": "number"}}
	switch name {
	case "director_block_scene":
		return map[string]any{"type": "object", "properties": map[string]any{"canvasSnapshotHash": str("最新画布快照"), "sceneId": str("新场景稳定 ID"), "title": str("场景标题"), "templateId": map[string]any{"type": "string", "enum": []string{"empty", "monologue", "dialogue", "blocking", "product", "action_chase", "interior", "crowd"}}}, "required": []string{"canvasSnapshotHash", "sceneId", "title", "templateId"}, "additionalProperties": false}
	case "director_design_shot":
		return map[string]any{"type": "object", "properties": map[string]any{"snapshotHash": str("场景快照"), "sceneId": str("场景 ID"), "shotId": str("镜头 ID"), "shotSize": map[string]any{"type": "string", "enum": []string{"extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"}}, "cameraMove": map[string]any{"type": "string", "enum": []string{"static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"}}, "prompt": str("镜头意图")}, "required": []string{"snapshotHash", "sceneId", "shotId"}, "additionalProperties": false}
	case "director_draw_action":
		return map[string]any{"type": "object", "properties": map[string]any{"snapshotHash": str("场景快照"), "sceneId": str("场景 ID"), "objectId": str("演员或对象 ID"), "pathPoints": map[string]any{"type": "array", "minItems": 2, "maxItems": 64, "items": vec}, "pathSpeed": map[string]any{"type": "string", "enum": []string{"slow", "walk", "run"}}, "startDelay": map[string]any{"type": "number", "minimum": 0, "maximum": 60}, "orientToPath": map[string]any{"type": "boolean"}}, "required": []string{"snapshotHash", "sceneId", "objectId", "pathPoints"}, "additionalProperties": false}
	case "director_check_continuity":
		return map[string]any{"type": "object", "properties": map[string]any{"sceneId": str("场景 ID"), "shotId": str("可选镜头 ID")}, "required": []string{"sceneId"}, "additionalProperties": false}
	case "director_preview_shot":
		return map[string]any{"type": "object", "properties": map[string]any{"sceneId": str("场景 ID"), "shotId": str("镜头 ID"), "duration": map[string]any{"type": "number", "minimum": 0.1, "maximum": 60}, "fps": map[string]any{"type": "integer", "minimum": 1, "maximum": 60}}, "required": []string{"sceneId", "shotId"}, "additionalProperties": false}
	case "director_handoff_shot":
		return map[string]any{"type": "object", "properties": map[string]any{"sceneId": str("场景 ID"), "shotId": str("镜头 ID")}, "required": []string{"sceneId", "shotId"}, "additionalProperties": false}
	default:
		return map[string]any{}
	}
}

func cloudAgentDirectorRead(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (any, error) {
	var args struct {
		SceneID string `json:"sceneId"`
		ShotID  string `json:"shotId"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if err := validateCloudAgentID(args.SceneID, "导演场景 ID", 80); err != nil {
		return nil, err
	}
	read := cloudAgentCall{ID: call.ID}
	read.Function.Name = "previs_scene_read"
	encoded, _ := json.Marshal(map[string]any{"sceneId": args.SceneID, "shotId": args.ShotID, "includeTransforms": true})
	read.Function.Arguments = string(encoded)
	projection, err := cloudAgentPrevisSceneRead(repo, userID, canvasID, read)
	if err != nil {
		return nil, err
	}
	if call.Function.Name == "director_handoff_shot" {
		return map[string]any{"sceneId": args.SceneID, "shotId": args.ShotID, "handoff": map[string]any{"scene": projection, "prompt": directorHandoffPrompt(projection), "execution": "existing_previs_prompt_compiler"}}, nil
	}
	issues := []string{}
	if args.ShotID == "" {
		issues = append(issues, "未指定镜头；请为每个镜头单独检查接拍")
	} else if scene, ok := projection.(map[string]any); ok {
		shots := creationMaps(scene["shots"])
		cameras := creationMaps(scene["cameras"])
		cameraIDs := map[string]bool{}
		for _, camera := range cameras {
			cameraIDs[stringValue(camera["id"])] = true
		}
		for _, shot := range shots {
			if stringValue(shot["id"]) != args.ShotID {
				continue
			}
			cameraID := stringValue(shot["cameraId"])
			if cameraID == "" || !cameraIDs[cameraID] {
				issues = append(issues, "镜头缺少有效摄影机")
			}
			if numberValue(shot["duration"], 0) <= 0 {
				issues = append(issues, "镜头时长必须大于零")
			}
		}
		for _, object := range creationMaps(scene["objects"]) {
			if path, ok := object["motionPath"].(map[string]any); ok && len(creationMaps(path["points"])) < 2 {
				issues = append(issues, "运动路径至少需要两个点")
			}
		}
	}
	return map[string]any{"sceneId": args.SceneID, "shotId": args.ShotID, "status": map[string]any{"ok": len(issues) == 0, "issues": issues}, "scene": projection, "repairRequiresApproval": true}, nil
}

func directorHandoffPrompt(value any) string {
	encoded, _ := json.Marshal(value)
	text := strings.TrimSpace(string(encoded))
	if len(text) > 4000 {
		text = text[:4000]
	}
	return "根据以下导演摘要继续设计镜头，不要假设未提供的资产或动作：" + text
}
