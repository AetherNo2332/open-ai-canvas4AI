package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const previsDefaultActorURL = "https://cdn.jsdelivr.net/gh/mrdoob/three.js@r185/examples/models/gltf/Xbot.glb"

type previsRenderInput struct {
	CanvasID           string            `json:"canvasId"`
	SceneID            string            `json:"sceneId"`
	ShotID             string            `json:"shotId"`
	CallID             string            `json:"callId"`
	SourceHash         string            `json:"sourceHash"`
	Scene              map[string]any    `json:"scene"`
	Duration           float64           `json:"duration"`
	FPS                int               `json:"fps"`
	Width              int               `json:"width"`
	Height             int               `json:"height"`
	Assets             map[string]string `json:"assets"`
	RepairSourceTaskID string            `json:"repairSourceTaskId,omitempty"`
	RepairMode         string            `json:"repairMode,omitempty"`
}

func findPrevisSceneCamera(scene map[string]any, id string) (map[string]any, bool) {
	for _, camera := range creationMaps(scene["cameras"]) {
		if stringValue(camera["id"]) == id {
			return camera, true
		}
	}
	return nil, false
}

// Ignore UI state and other shots, while retaining every input used by this camera.
func previsRenderSourceHash(scene map[string]any, shotID string) string {
	shot, ok := findPrevisShot(scene, shotID)
	if !ok {
		return ""
	}
	camera, ok := findPrevisSceneCamera(scene, stringValue(shot["cameraId"]))
	if !ok {
		return ""
	}
	selectFields := func(source map[string]any, fields ...string) map[string]any {
		out := map[string]any{}
		for _, field := range fields {
			if value, exists := source[field]; exists {
				out[field] = value
			}
		}
		return out
	}
	objects := []any{}
	for _, object := range creationMaps(scene["objects"]) {
		if !boolValue(object["visible"], true) {
			continue
		}
		projection := selectFields(object, "id", "kind", "primitive", "transform", "color", "visible", "castShadow", "receiveShadow", "pose", "archetype", "actorProfile", "rig", "motionClips", "activeMotionClipId", "boneOverrides", "boneTracks", "assetId", "storageKey", "url", "keyframes", "motionPath")
		projection["color"] = cloudAgentPrevisNormalizeSceneColor(stringValue(object["color"]), "#8795a5")
		objects = append(objects, projection)
	}
	sceneProjection := selectFields(scene, "id", "version", "environmentIntensity", "environment")
	sceneProjection["background"] = cloudAgentPrevisNormalizeSceneColor(stringValue(scene["background"]), "#111827")
	lights := []any{}
	for _, light := range creationMaps(scene["lights"]) {
		projection := selectFields(light, "id", "type", "transform", "color", "intensity", "angle", "penumbra", "castShadow")
		projection["color"] = cloudAgentPrevisNormalizeSceneColor(stringValue(light["color"]), "#ffffff")
		lights = append(lights, projection)
	}
	sceneProjection["lights"] = lights
	return creationHash(map[string]any{
		"scene":   sceneProjection,
		"objects": objects,
		"camera":  selectFields(camera, "id", "transform", "target", "focalLength", "fov", "near", "far", "keyframes", "motionPath"),
		"shot":    selectFields(shot, "id", "cameraId", "duration", "fps", "aspectRatio", "cameraMove"),
	})
}

func preparePrevisRender(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (previsRenderInput, error) {
	input := previsRenderInput{}
	result, err := cloudAgentPrevisPreview(repo, userID, canvasID, call)
	if err != nil {
		return input, err
	}
	metadata := result.(map[string]any)
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return input, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return input, err
	}
	scene, _ := findPrevisScene(creationMaps(doc["previsScenes"]), stringValue(metadata["sceneId"]))
	if err := cloudAgentPrevisValidateScene(scene); err != nil {
		return input, err
	}
	input = previsRenderInput{CanvasID: canvasID, SceneID: stringValue(metadata["sceneId"]), ShotID: stringValue(metadata["shotId"]), CallID: call.ID,
		Duration: numberValue(metadata["duration"], 5), FPS: int(numberValue(metadata["fps"], 24)), Scene: scene, Assets: map[string]string{}, Width: 360, Height: 640}
	input.SourceHash = previsRenderSourceHash(scene, input.ShotID)
	shot, _ := findPrevisShot(scene, input.ShotID)
	switch stringValue(shot["aspectRatio"]) {
	case "16:9":
		input.Width, input.Height = 640, 360
	case "2.39:1":
		input.Width, input.Height = 640, 268
	}
	// URLs in frozen task inputs are reduced to owned resource IDs or the bundled actor.
	bind := func(item map[string]any, name string, actor bool) error {
		key, url := stringValue(item["storageKey"]), stringValue(item["url"])
		if actor && key == "" && (url == "" || url == previsDefaultActorURL) && stringValue(item["assetId"]) == "" {
			if url == "" {
				item["url"] = "/models/Xbot.glb"
			}
			return nil
		}
		if !strings.HasPrefix(key, "resource:") {
			return BadAuthRequest("预演素材缺少已导入的资源引用，请先将素材保存到当前账号")
		}
		resourceID := strings.TrimPrefix(key, "resource:")
		resource, err := repo.ResourceForUser(userID, resourceID)
		if err != nil || resource == nil || resource.Status != model.ResourceStatusReady || resource.Size <= 0 || resource.Size > 64<<20 {
			return BadAuthRequest("预演素材不存在或当前账号无权访问")
		}
		input.Assets[name] = resourceID
		item["url"], item["storageKey"] = "/job-assets/"+name, ""
		return nil
	}
	for index, object := range creationMaps(scene["objects"]) {
		if !boolValue(object["visible"], true) {
			delete(object, "url")
			delete(object, "storageKey")
			continue
		}
		kind := stringValue(object["kind"])
		if kind == "actor" || kind == "model" || kind == "billboard" || stringValue(object["primitive"]) == "character" {
			if err := bind(object, fmt.Sprintf("object-%d", index), kind == "actor" || stringValue(object["primitive"]) == "character"); err != nil {
				return previsRenderInput{}, err
			}
		}
	}
	if environment, ok := scene["environment"].(map[string]any); ok && stringValue(environment["mode"]) == "panorama" {
		if err := bind(environment, "environment", false); err != nil {
			return previsRenderInput{}, err
		}
	}
	return input, nil
}

func (s *Service) advanceCloudAgentPrevis(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall, policy RuntimePolicySetting) error {
	if cloudAgentRunTerminal(run.Status) {
		return nil
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	if state.MediaTaskID != "" {
		task, err := s.repo.TaskForUser(run.UserID, state.MediaTaskID)
		if err != nil {
			return err
		}
		if !cloudAgentTaskTerminal(task.Status) {
			return nil
		}
		return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			result := map[string]any{}
			_ = json.Unmarshal([]byte(task.ResultJSON), &result)
			result["taskId"], result["status"], result["execution"] = task.ID, task.Status, "server_previs_renderer"
			result["phase"], result["taskSubmitted"], result["retryClass"] = "completion", true, "manual"
			var toolErr error
			if task.Status != model.TaskStatusSucceeded || stringValue(result["videoNodeId"]) == "" || stringValue(result["resourceId"]) == "" {
				toolErr = BadAuthRequest("后台预演未交付：" + cloudAgentSafeMediaTaskError(task) + "；请按任务ID检查结果，不会自动重复渲染")
			}
			cloudAgentRecordToolResult(current, state, call, result, toolErr)
			state.MediaTaskID = ""
			current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
			return cloudAgentSave(current, state)
		})
	}
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		input, err := preparePrevisRender(repo, run.UserID, state.Request.CanvasID, call)
		if err != nil {
			cloudAgentRecordToolResult(current, state, call, nil, err)
			return cloudAgentSave(current, state)
		}
		if state.Approval != nil && state.Approval.PrevisSourceHash != input.SourceHash {
			cloudAgentRecordToolResult(current, state, call, nil, BadAuthRequest("审批后目标镜头已变化，请重新读取并审批预演"))
			return cloudAgentSave(current, state)
		}
		id := cloudAgentID(run.UserID, run.ID+":previs:"+state.PiToolBatchTaskID+fmt.Sprintf(":%d:", state.Step)+call.ID)
		task, err := repo.TaskForUser(run.UserID, id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			raw, err := json.Marshal(input)
			if err != nil {
				return err
			}
			if len(raw) > cloudAgentPrevisDocumentLimit {
				return BadAuthRequest("预演输入大小超限")
			}
			task = &model.Task{ID: id, UserID: run.UserID, ProjectID: state.Request.CanvasID, AgentRunID: run.ID, Type: model.TaskTypePrevisRender, Status: model.TaskStatusQueued, Stage: "预演等待队列调度", Provider: "local", Model: "previs-renderer", Prompt: "导演台白模预演", InputJSON: string(raw)}
			if state.Approval != nil {
				task.ApprovalID = state.Approval.ID
			}
			if err := createTaskWithStorageQuotaRepository(repo, task, nil, policy); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if task.AgentRunID != run.ID || task.Type != model.TaskTypePrevisRender {
			return BadAuthRequest("预演任务身份冲突")
		}
		resources := make([]string, 0, len(input.Assets))
		for _, id := range input.Assets {
			resources = append(resources, id)
		}
		if err := repo.UpsertCloudAgentResourceLeases(run.UserID, run.ID, "previs-task:"+task.ID, resources, time.Now().Add(45*time.Minute)); err != nil {
			return err
		}
		state.MediaTaskID = task.ID
		if !cloudAgentContainsString(state.TaskIDs, task.ID) {
			state.TaskIDs = append(state.TaskIDs, task.ID)
		}
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "waiting_tool", "previs", task.ID, "等待服务器生成并回写预演"
		state.event(run.ID, "previs_task_created", map[string]any{"callId": call.ID, "taskId": task.ID, "canvasId": input.CanvasID, "sceneId": input.SceneID, "shotId": input.ShotID, "status": task.Status, "execution": "server_previs_renderer", "text": "后台预演已排队，等待真实视频和画布节点"})
		return cloudAgentSave(current, state)
	})
}
