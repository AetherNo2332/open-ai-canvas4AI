package commands

import (
	"fmt"
	"math"
	"strings"
	"yingce/backend/internal/assets"
)

type PrevisRenderInput = previsRenderInput
type PrevisOutput struct {
	ResourceID        string `json:"resourceId"`
	PreviewResourceID string `json:"previewResourceId"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	DurationMs        int64  `json:"durationMs"`
	FrameCount        int    `json:"frameCount"`
}

// The identity fingerprint has a semantic use: reject writes to a shot whose
// render inputs changed while the renderer was working. It ignores UI state.
func PrevisSourceHash(scene map[string]any, shot string) string {
	return (&planner{}).previsRenderSourceHash(scene, shot)
}
func PrevisScene(doc Document, id string) map[string]any {
	for _, scene := range maps(doc["previsScenes"]) {
		if text(scene["id"]) == id {
			return scene
		}
	}
	return nil
}
func PreparePrevisRender(doc Document, canvas, sceneID, shotID, op string, options map[string]any) (PrevisRenderInput, error) {
	for _, id := range []string{sceneID, shotID} {
		if err := validateCanvasID(id, "预演目标", 80); err != nil {
			return PrevisRenderInput{}, err
		}
	}
	frozen, err := cloneDocument(doc)
	if err != nil {
		return PrevisRenderInput{}, err
	}
	scene := PrevisScene(frozen, sceneID)
	if scene == nil {
		return PrevisRenderInput{}, failure("entity_deleted", "预演场景不存在")
	}
	p := &planner{}
	if err = p.canvasPrevisValidateScene(scene); err != nil {
		return PrevisRenderInput{}, err
	}
	shot, ok := p.findPrevisShot(scene, shotID)
	if !ok {
		return PrevisRenderInput{}, failure("entity_deleted", "预演镜头不存在")
	}
	duration, fps := p.numberValue(shot["duration"], 5), int(p.numberValue(shot["fps"], 24))
	for key, value := range options {
		switch key {
		case "duration":
			duration = p.numberValue(value, math.NaN())
		case "fps":
			n := p.numberValue(value, math.NaN())
			if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
				return PrevisRenderInput{}, BadAuthRequest("帧率必须是整数")
			}
			fps = int(n)
		default:
			return PrevisRenderInput{}, BadAuthRequest("预演参数不支持 " + key)
		}
	}
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0.1 || duration > 60 || fps < 1 || fps > 60 {
		return PrevisRenderInput{}, BadAuthRequest("预演时长或帧率无效")
	}
	if _, ok = p.findPrevisSceneCamera(scene, text(shot["cameraId"])); !ok {
		return PrevisRenderInput{}, BadAuthRequest("预演缺少摄影机")
	}
	input := PrevisRenderInput{CanvasID: canvas, SceneID: sceneID, ShotID: shotID, CallID: op, SourceHash: p.previsRenderSourceHash(scene, shotID), Scene: scene, Duration: duration, FPS: fps, Width: 360, Height: 640, Assets: map[string]string{}}
	switch text(shot["aspectRatio"]) {
	case "16:9":
		input.Width, input.Height = 640, 360
	case "2.39:1":
		input.Width, input.Height = 640, 268
	}
	bind := func(item map[string]any, name string, actor bool) error {
		key, url := text(item["storageKey"]), text(item["url"])
		if actor && key == "" && (url == "" || url == previsDefaultActorURL || url == "/models/Xbot.glb") && text(item["assetId"]) == "" {
			item["url"] = "/models/Xbot.glb"
			return nil
		}
		id := assets.ResourceID(key)
		if !strings.HasPrefix(key, "resource:") || id == "" {
			return BadAuthRequest("预演素材需先导入本地资源")
		}
		input.Assets[name] = id
		item["url"], item["storageKey"] = "/job-assets/"+name, ""
		return nil
	}
	for i, obj := range maps(scene["objects"]) {
		if !p.boolValue(obj["visible"], true) {
			delete(obj, "url")
			delete(obj, "storageKey")
			continue
		}
		kind := text(obj["kind"])
		actor := kind == "actor" || text(obj["primitive"]) == "character"
		if actor || kind == "model" || kind == "billboard" {
			if err = bind(obj, fmt.Sprintf("object-%d", i), actor); err != nil {
				return PrevisRenderInput{}, err
			}
		}
	}
	if env, ok := scene["environment"].(map[string]any); ok && text(env["mode"]) == "panorama" {
		if err = bind(env, "environment", false); err != nil {
			return PrevisRenderInput{}, err
		}
	}
	return input, nil
}
func ApplyPrevisResult(doc Document, input PrevisRenderInput, taskID string, out PrevisOutput) (Document, string, error) {
	next, err := cloneDocument(doc)
	if err != nil {
		return nil, "", err
	}
	scene := PrevisScene(next, input.SceneID)
	if scene == nil {
		return next, "scene_deleted", nil
	}
	if PrevisSourceHash(scene, input.ShotID) != input.SourceHash {
		return next, "source_changed", nil
	}
	if out.ResourceID == "" || out.PreviewResourceID == "" || out.Width != input.Width || out.Height != input.Height {
		return nil, "", BadAuthRequest("预演结果不符")
	}
	videoID, previewID := "previs-video-"+taskID, "previs-preview-"+taskID
	nodes := maps(next["nodes"])
	for i, item := range []struct{ id, resource, kind, title string }{{videoID, out.ResourceID, "video", "白模预演"}, {previewID, out.PreviewResourceID, "image", "预演构图帧"}} {
		if entity(next, "node:"+item.id) != nil {
			return nil, "", failure("entity_version_conflict", "预演输出节点 ID 已存在")
		}
		m := map[string]any{"content": assets.FileURL(item.resource), "storageKey": "resource:" + item.resource, "status": "success", "taskId": taskID, "taskStatus": "succeeded", "previsSceneId": input.SceneID, "previsShotId": input.ShotID, "previsSourceHash": input.SourceHash, "previsIndependent": false, "naturalWidth": out.Width, "naturalHeight": out.Height, "durationMs": out.DurationMs}
		nodes = append(nodes, map[string]any{"id": item.id, "type": item.kind, "title": item.title, "position": map[string]any{"x": float64(i * 380), "y": float64(len(nodes) / 2 * 300)}, "width": 320.0, "height": 320 * float64(out.Height) / float64(out.Width), "metadata": m})
	}
	next["nodes"] = nodes
	shot, _ := (&planner{}).findPrevisShot(scene, input.ShotID)
	shot["clayVideoNodeId"], shot["previewNodeId"] = videoID, previewID
	return next, "applied", nil
}
