package app

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"yingce/backend/internal/repository"
)

func cloudAgentPrevisSceneCreateTemplate(args cloudAgentPrevisSceneCreateArgs) (map[string]any, error) {
	if err := validateCloudAgentID(args.SceneID, "导演场景 ID", 80); err != nil {
		return nil, err
	}
	if err := cloudAgentPrevisValidateText(args.Title, "场景标题", cloudAgentPrevisTextLimit, true); err != nil {
		return nil, err
	}
	if err := cloudAgentPrevisValidateTemplateID(args.TemplateID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cameraID := args.SceneID + "-camera-1"
	shotID := args.SceneID + "-shot-1"
	camera := map[string]any{
		"id": cameraID, "name": "主摄影机",
		"transform": map[string]any{"position": []any{4.8, 2.7, 6.8}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}},
		"target":    []any{0, 1, 0}, "focalLength": 35, "fov": 50, "aperture": 2.8, "focusDistance": 5, "near": 0.05, "far": 500, "keyframes": []any{},
	}
	objects := []any{}
	lights := []any{
		map[string]any{"id": args.SceneID + "-light-key", "name": "主光", "type": "directional", "transform": map[string]any{"position": []any{4, 6, 4}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#ffffff", "intensity": 2.4, "angle": 0.7853981634, "penumbra": 0.35, "castShadow": true},
		map[string]any{"id": args.SceneID + "-light-fill", "name": "轮廓光", "type": "directional", "transform": map[string]any{"position": []any{-4, 3, -2}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#ffffff", "intensity": 1.1, "angle": 0.7853981634, "penumbra": 0.35, "castShadow": true},
		map[string]any{"id": args.SceneID + "-light-ambient", "name": "环境光", "type": "ambient", "transform": map[string]any{"position": []any{0, 0, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#ffffff", "intensity": 0.65, "angle": 0.7853981634, "penumbra": 0.35, "castShadow": false},
	}
	if args.TemplateID == "action_chase" {
		// Two actors: pursuer behind, target ahead, side-angle camera for kinetic energy
		objects = append(objects,
			map[string]any{"id": args.SceneID + "-actor-1", "name": "追逐者", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{0, 0, -1.2}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#2f7de1", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "run", "archetype": "man", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}},
			map[string]any{"id": args.SceneID + "-actor-2", "name": "逃跑者", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{0, 0, 2.0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#d84949", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "run", "archetype": "adult", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}},
		)
		camera["transform"] = map[string]any{"position": []any{3.8, 1.4, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
		camera["target"] = []any{0, 1, 0.5}
		camera["focalLength"] = 50
		camera["fov"] = 39.6
	}
	if args.TemplateID == "interior" {
		// Room sketch: floor plane, 3 wall planes, sofa-scale box, table-scale box
		objects = append(objects,
			map[string]any{"id": args.SceneID + "-wall-back", "name": "后墙", "kind": "primitive", "primitive": "plane", "transform": map[string]any{"position": []any{0, 2.0, -3.0}, "rotation": []any{0, 0, 0}, "scale": []any{6, 4, 1}}, "color": "#d8dde3", "visible": true, "castShadow": false, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-wall-left", "name": "左墙", "kind": "primitive", "primitive": "plane", "transform": map[string]any{"position": []any{-3.0, 2.0, 0}, "rotation": []any{0, 1.5708, 0}, "scale": []any{6, 4, 1}}, "color": "#d8dde3", "visible": true, "castShadow": false, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-sofa", "name": "沙发", "kind": "primitive", "primitive": "box", "transform": map[string]any{"position": []any{0, 0.45, -1.8}, "rotation": []any{0, 0, 0}, "scale": []any{2.2, 0.9, 0.9}}, "color": "#8795a5", "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-table", "name": "茶几", "kind": "primitive", "primitive": "box", "transform": map[string]any{"position": []any{0, 0.23, 0.2}, "rotation": []any{0, 0, 0}, "scale": []any{1.1, 0.46, 0.65}}, "color": "#6b7580", "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-actor-1", "name": "演员 1", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{-0.6, 0, 0.6}, "rotation": []any{0, -0.4, 0}, "scale": []any{1, 1, 1}}, "color": "#2f7de1", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "sit", "archetype": "adult", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}},
		)
		camera["transform"] = map[string]any{"position": []any{3.2, 1.65, 3.0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
		camera["target"] = []any{-0.2, 1.0, 0}
		camera["focalLength"] = 35
	}
	if args.TemplateID == "crowd" {
		// 3x3 crowd grid with distinct colors
		crowdColors := []string{"#2f7de1", "#d84949", "#dfae3f", "#34a276", "#8b5cf6", "#e879f9", "#2f7de1", "#d84949", "#dfae3f"}
		for row := 0; row < 3; row++ {
			for col := 0; col < 3; col++ {
				idx := row*3 + col
				id := fmt.Sprintf("%s-actor-%d", args.SceneID, idx+1)
				x := float64(col-1) * 1.2
				z := float64(row-1) * 1.2
				objects = append(objects, map[string]any{
					"id": id, "name": fmt.Sprintf("群众 %d", idx+1), "kind": "actor", "primitive": "character",
					"transform": map[string]any{"position": []any{x, 0, z}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}},
					"color":     crowdColors[idx], "visible": true, "castShadow": true, "receiveShadow": true,
					"pose": "stand", "archetype": "adult", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{},
				})
			}
		}
		camera["transform"] = map[string]any{"position": []any{0.5, 4.5, 6.5}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
		camera["target"] = []any{0, 0.8, 0}
		camera["focalLength"] = 28
		camera["fov"] = 65.0
	}
	if args.TemplateID == "monologue" || args.TemplateID == "dialogue" || args.TemplateID == "blocking" {
		objects = append(objects, map[string]any{"id": args.SceneID + "-actor-1", "name": "演员 1", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{0, 0, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#f1f3f5", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "stand", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}})
	}
	if args.TemplateID == "dialogue" || args.TemplateID == "blocking" {
		objects = append(objects, map[string]any{"id": args.SceneID + "-actor-2", "name": "演员 2", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{1.8, 0, -0.8}, "rotation": []any{0, 0.2, 0}, "scale": []any{1, 1, 1}}, "color": "#2f7de1", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "stand", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}})
	}
	if args.TemplateID == "product" {
		objects = append(objects, map[string]any{"id": args.SceneID + "-product-1", "name": "产品", "kind": "primitive", "primitive": "box", "transform": map[string]any{"position": []any{0, 0.6, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1.2, 1.2, 1.2}}, "color": "#8795a5", "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}})
	}
	scene := map[string]any{
		"id": args.SceneID, "version": 1, "title": args.Title, "background": "#d8dde3", "environmentIntensity": 0.7, "gridVisible": true,
		"objects": objects, "cameras": []any{camera}, "lights": lights,
		"shots":        []any{map[string]any{"id": shotID, "name": "镜头 1", "cameraId": cameraID, "duration": 5, "fps": 24, "shotSize": "medium", "cameraMove": "static", "prompt": "", "cuts": []any{}}},
		"activeShotId": shotID, "createdAt": now, "updatedAt": now,
	}
	return scene, cloudAgentPrevisValidateScene(scene)
}

func prepareCloudAgentPrevisSceneCreate(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentPrevisMutationPlan, error) {
	var args cloudAgentPrevisSceneCreateArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if strings.TrimSpace(args.CanvasSnapshotHash) == "" {
		return nil, cloudAgentFieldError("canvasSnapshotHash", "required", "创建预演场景前必须先读取场景目录")
	}
	canvas, doc, err := cloudAgentPrevisCanvas(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	beforeHash := cloudAgentCanvasHash(doc)
	if beforeHash != args.CanvasSnapshotHash {
		return nil, &cloudAgentFieldArgumentError{error: &cloudAgentArgumentError{creationConflict("画布已变化，本次未创建场景；请重新读取场景目录")}, Field: "canvasSnapshotHash", Issue: "stale_snapshot"}
	}
	scenes := creationMaps(doc["previsScenes"])
	if len(scenes) >= cloudAgentPrevisMaxScenes {
		return nil, BadAuthRequest("当前画布的预演场景数量已达到限制")
	}
	if _, exists := findPrevisScene(scenes, args.SceneID); exists {
		return nil, BadAuthRequest("预演场景 ID 已存在")
	}
	scene, err := cloudAgentPrevisSceneCreateTemplate(args)
	if err != nil {
		return nil, err
	}
	scenes = append(scenes, scene)
	if err := cloudAgentPrevisScenesSize(scenes); err != nil {
		return nil, err
	}
	doc["previsScenes"] = scenes
	return &cloudAgentPrevisMutationPlan{Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: beforeHash, AfterSnapshotHash: cloudAgentCanvasHash(doc), ResultSnapshotHash: creationHash(scene), Operation: "previs_scene_create", SceneID: args.SceneID, Preview: cloudAgentPrevisMutationApprovalPreview("previs_scene_create", args.SceneID, args.Title, []string{"创建受控白模场景", "模板：" + args.TemplateID})}, nil
}

func prepareCloudAgentPrevisApplyPatch(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentPrevisMutationPlan, error) {
	var args cloudAgentPrevisApplyPatchArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if strings.TrimSpace(args.SnapshotHash) == "" {
		return nil, cloudAgentFieldError("snapshotHash", "required", "应用预演补丁前必须先读取指定场景")
	}
	if len(args.Operations) < 1 || len(args.Operations) > cloudAgentPrevisMaxOperations {
		return nil, cloudAgentFieldError("operations", "item_count", fmt.Sprintf("预演补丁 operations 必须包含1到%d项操作", cloudAgentPrevisMaxOperations))
	}
	if err := validateCloudAgentID(args.SceneID, "导演场景 ID", 80); err != nil {
		return nil, err
	}
	canvas, doc, err := cloudAgentPrevisCanvas(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	scenes := creationMaps(doc["previsScenes"])
	scene, ok := findPrevisScene(scenes, args.SceneID)
	if !ok {
		return nil, BadAuthRequest("导演场景不存在或不属于当前画布")
	}
	canvasSnapshotHash := cloudAgentCanvasHash(doc)
	sceneSnapshotHash := creationHash(scene)
	if sceneSnapshotHash != args.SnapshotHash {
		return nil, &cloudAgentFieldArgumentError{error: &cloudAgentArgumentError{creationConflict("预演场景已变化，本次未写入；请重新读取并重新申请审批")}, Field: "snapshotHash", Issue: "stale_snapshot"}
	}
	items := make([]cloudAgentApprovalPreviewItem, 0, len(args.Operations))
	for index, operation := range args.Operations {
		if err := cloudAgentPrevisValidateCharacterBindingInCanvas(repo, userID, doc, operation); err != nil {
			return nil, cloudAgentPrevisPatchOperationError(index, err)
		}
		item, err := cloudAgentPrevisApplyPatchOperation(scene, operation, index)
		if err != nil {
			return nil, cloudAgentPrevisPatchOperationError(index, err)
		}
		items = append(items, item)
	}
	cloudAgentPrevisGroundScene(scene)
	scene["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := cloudAgentPrevisValidateScene(scene); err != nil {
		return nil, err
	}
	if err := cloudAgentPrevisScenesSize(scenes); err != nil {
		return nil, err
	}
	doc["previsScenes"] = scenes
	return &cloudAgentPrevisMutationPlan{Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: canvasSnapshotHash, AfterSnapshotHash: cloudAgentCanvasHash(doc), ResultSnapshotHash: creationHash(scene), Operation: "previs_apply_patch", SceneID: args.SceneID, Preview: cloudAgentPrevisMutationApprovalPreview("previs_apply_patch", args.SceneID, stringValue(scene["title"]), itemsToDetails(items))}, nil
}

// cloudAgentPrevisPatchOperationError turns an operation-level validation
// rejection into a model-visible field error. The model can identify and fix
// the failed operation in the next step instead of losing the whole run.
// Repository and other infrastructure errors keep their original type so the
// surrounding transaction still rolls back.
func cloudAgentPrevisPatchOperationError(index int, err error) error {
	var argumentErr *cloudAgentArgumentError
	if errors.As(err, &argumentErr) {
		return err
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr == nil || (appErr.Status != 400 && appErr.Status != 422) {
		return err
	}
	field := fmt.Sprintf("operations[%d]", index)
	return &cloudAgentFieldArgumentError{
		error: &cloudAgentArgumentError{fmt.Errorf("%s: %w", field, err)},
		Field: field,
		Issue: "invalid_value",
	}
}

func itemsToDetails(items []cloudAgentApprovalPreviewItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item.Summary != "" {
			result = append(result, item.Summary)
		}
	}
	return result
}

func cloudAgentPrevisMutationApprovalPreview(operation, sceneID, title string, details []string) cloudAgentApprovalPreview {
	if len(details) > 12 {
		details = details[:12]
	}
	return cloudAgentApprovalPreview{Kind: "previs_mutation", Title: "确认预演台修改", Description: fmt.Sprintf("Agent 准备修改预演场景《%s》。批准后才会写入当前画布。", truncateRunes(title, cloudAgentPrevisTextLimit)), Items: []cloudAgentApprovalPreviewItem{{Operation: operation, NodeID: sceneID, NodeTitle: truncateRunes(title, cloudAgentPrevisTextLimit), Details: details, Summary: fmt.Sprintf("修改预演场景《%s》", truncateRunes(title, cloudAgentPrevisTextLimit))}}}
}
