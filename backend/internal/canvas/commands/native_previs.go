package commands

import (
	"infinite-canvas/backend/internal/assets"
	"math"
	"strconv"
	"strings"
	"time"
)

// These fields describe the persisted Canary editor, including rig and optical
// controls absent from the older convenience operations. Unknown stored fields
// survive; clients cannot use an entity patch to write runtime/task state.
var previsFields = map[string]map[string]string{
	"settings":     {"title": "text", "background": "color", "environmentIntensity": "range:0:100", "environment": "environment", "gridVisible": "bool", "activeShotId": "id"},
	"object":       {"name": "text", "kind": "enum:primitive,model,actor,billboard", "primitive": "enum:box,sphere,cylinder,plane,character", "transform": "transform", "color": "color", "visible": "bool", "castShadow": "bool", "receiveShadow": "bool", "pose": "pose", "archetype": "enum:adult,child,elderly,man,woman,monster", "actorProfile": "actorProfile", "rig": "rig", "motionClips": "array:motionClip", "activeMotionClipId": "id", "characterBinding": "binding", "boneOverrides": "boneQuats", "boneTracks": "array:boneTrack", "sourceNodeId": "id", "assetId": "id", "storageKey": "locator", "url": "modelLocator", "mimeType": "text", "keyframes": "array:keyframe", "motionPath": "motionPath"},
	"camera":       {"name": "text", "transform": "transform", "target": "vec3", "focalLength": "range:1:300", "fov": "range:0.1:179", "aperture": "range:0.1:128", "focusDistance": "range:0.001:1000000", "near": "range:0.00001:1000000", "far": "range:0.00001:1000000", "keyframes": "array:keyframe", "motionPath": "motionPath"},
	"light":        {"name": "text", "type": "enum:directional,point,spot,ambient", "transform": "transform", "color": "color", "intensity": "range:0:100", "angle": "range:0:6.29", "penumbra": "range:0:1", "castShadow": "bool"},
	"shot":         {"name": "text", "cameraId": "id", "duration": "range:0.1:60", "fps": "enumNumber:24,25,30", "aspectRatio": "enum:9:16,16:9,2.39:1", "shotSize": "enum:extreme_wide,wide,full,medium,close_up,extreme_close_up", "cameraMove": "enum:static,push_in,pull_out,pan_left,pan_right,tilt_up,tilt_down,orbit_left,orbit_right,handheld", "prompt": "prompt", "previewNodeId": "id", "clayVideoNodeId": "id", "depthNodeId": "id", "normalNodeId": "id"},
	"environment":  {"mode": "enum:color,panorama", "url": "locator", "storageKey": "locator", "sourceNodeId": "id", "rotationY": "range:-1000000:1000000", "opacity": "range:0:1", "exposure": "range:-100:100", "useAsLighting": "bool", "lightingIntensity": "range:0:100"},
	"transform":    {"position": "vec3", "rotation": "vec3", "scale": "vec3"},
	"actorProfile": {"height": "range:0.01:100", "headRatio": "range:0.001:10", "shoulderWidth": "range:0.001:100", "torsoRatio": "range:0.001:10", "accessory": "enum:none,cane,horns,crown"},
	"rig":          {"status": "enum:unmapped,ready", "boneMap": "boneNames", "animationNames": "array:text"},
	"motionClip":   {"id": "id", "name": "text", "sourceAnimation": "text", "start": "range:0:60", "duration": "range:0.001:3600", "playbackRate": "range:0.001:100", "loop": "bool"},
	"binding":      {"characterAssetId": "id", "characterVersionId": "id", "referenceNodeId": "id", "characterName": "text"},
	"boneTrack":    {"bone": "bone", "keyframes": "array:boneKeyframe"},
	"boneKeyframe": {"id": "id", "time": "range:0:60", "rotation": "quat", "easing": "enum:step,linear,smooth"},
	"keyframe":     {"id": "id", "time": "range:0:60", "transform": "transform", "easing": "enum:step,linear,smooth"},
	"motionPath":   {"points": "array:vec3", "speed": "enum:slow,walk,run", "startDelay": "range:0:60", "orientToPath": "bool"},
}

var previsRequired = map[string][]string{
	"transform": {"position", "rotation", "scale"}, "actorProfile": {"height", "headRatio", "shoulderWidth", "torsoRatio", "accessory"},
	"rig": {"status", "boneMap", "animationNames"}, "motionClip": {"id", "name", "sourceAnimation", "start", "duration", "playbackRate", "loop"},
	"binding": {"characterAssetId", "characterVersionId", "referenceNodeId"}, "boneTrack": {"bone", "keyframes"},
	"boneKeyframe": {"id", "time", "rotation"}, "keyframe": {"id", "time", "transform"},
	"motionPath": {"points", "speed", "startDelay", "orientToPath"}, "environment": {"mode"},
}

func validatePrevisRequired(object map[string]any, kind string) error {
	for _, key := range previsRequired[kind] {
		if object[key] == nil || object[key] == "" {
			return BadAuthRequest("预演必填字段缺失：" + kind + "." + key)
		}
	}
	if kind == "motionPath" {
		points, _ := object["points"].([]any)
		if len(points) < 2 || len(points) > 64 {
			return BadAuthRequest("运动路径需要 2 至 64 个点")
		}
	}
	return nil
}

func previsBone(value string) bool {
	if strings.Contains("|root|hips|spine|chest|neck|head|leftShoulder|leftUpperArm|leftLowerArm|leftHand|rightShoulder|rightUpperArm|rightLowerArm|rightHand|leftUpperLeg|leftLowerLeg|leftFoot|rightUpperLeg|rightLowerLeg|rightFoot|", "|"+value+"|") {
		return value != ""
	}
	for _, side := range []string{"left", "right"} {
		for _, finger := range []string{"Thumb", "Index", "Middle", "Ring", "Pinky"} {
			for _, n := range []string{"1", "2", "3"} {
				if value == side+finger+n {
					return true
				}
			}
		}
	}
	return false
}

func (p *planner) validatePrevisValue(value any, rule string) error {
	bad := func() error { return BadAuthRequest("预演字段类型或范围无效：" + rule) }
	if fields, ok := previsFields[rule]; ok {
		object, ok := value.(map[string]any)
		if !ok {
			return bad()
		}
		if err := validatePrevisRequired(object, rule); err != nil {
			return err
		}
		for key, v := range object {
			r, ok := fields[key]
			if !ok {
				return BadAuthRequest("预演字段不可编辑：" + key)
			}
			if v == nil {
				continue
			}
			if err := p.validatePrevisValue(v, r); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.HasPrefix(rule, "array:") {
		values, ok := value.([]any)
		if !ok || len(values) > 10000 {
			return bad()
		}
		seen := map[string]bool{}
		for _, v := range values {
			if err := p.validatePrevisValue(v, strings.TrimPrefix(rule, "array:")); err != nil {
				return err
			}
			if m, ok := v.(map[string]any); ok {
				id := text(m["id"])
				if id == "" {
					id = text(m["bone"])
				}
				if id != "" {
					if seen[id] {
						return bad()
					}
					seen[id] = true
				}
			}
		}
		return nil
	}
	if strings.HasPrefix(rule, "enum:") {
		for _, s := range strings.Split(strings.TrimPrefix(rule, "enum:"), ",") {
			if value == s {
				return nil
			}
		}
		return bad()
	}
	if strings.HasPrefix(rule, "range:") {
		parts := strings.Split(rule, ":")
		low, _ := strconv.ParseFloat(parts[1], 64)
		high, _ := strconv.ParseFloat(parts[2], 64)
		n, ok := value.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < low || n > high {
			return bad()
		}
		return nil
	}
	switch rule {
	case "enumNumber:24,25,30":
		if value != float64(24) && value != float64(25) && value != float64(30) {
			return bad()
		}
	case "text", "prompt":
		s, ok := value.(string)
		if !ok || len(s) > 20000 {
			return bad()
		}
	case "id":
		s, ok := value.(string)
		if !ok {
			return bad()
		}
		if s != "" {
			return validateCanvasID(s, "预演引用", 128)
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return bad()
		}
	case "color":
		return p.canvasPrevisValidateColor(text(value), "预演颜色")
	case "pose":
		return p.canvasPrevisValidatePose(text(value))
	case "bone":
		if !previsBone(text(value)) {
			return bad()
		}
	case "locator", "modelLocator":
		s, ok := value.(string)
		if !ok {
			return bad()
		}
		if rule == "modelLocator" && s == previsDefaultActorURL {
			return nil
		}
		if s != "" && assets.ResourceID(s) == "" {
			return failure("resource_not_owned", "预演只接受已上传资源")
		}
	case "vec3", "quat":
		a, ok := value.([]any)
		size := 3
		if rule == "quat" {
			size = 4
		}
		if !ok || len(a) != size {
			return bad()
		}
		norm := 0.0
		for _, v := range a {
			n, ok := v.(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e6 {
				return bad()
			}
			norm += n * n
		}
		if rule == "quat" && math.Abs(norm-1) > 0.01 {
			return bad()
		}
	case "boneQuats", "boneNames":
		m, ok := value.(map[string]any)
		if !ok {
			return bad()
		}
		for bone, v := range m {
			if !previsBone(bone) {
				return bad()
			}
			r := "quat"
			if rule == "boneNames" {
				r = "text"
			}
			if err := p.validatePrevisValue(v, r); err != nil {
				return err
			}
		}
	default:
		return bad()
	}
	return nil
}

func (p *planner) patchPrevisEntity(target, patch map[string]any, kind string) error {
	if len(patch) == 0 {
		return BadAuthRequest("预演修改不能为空")
	}
	fields := previsFields[kind]
	for key, value := range patch {
		rule, ok := fields[key]
		if !ok {
			return BadAuthRequest("预演字段不可编辑：" + key)
		}
		if value == nil {
			delete(target, key)
			continue
		}
		// Nested records use merge patches so an unrelated setting cannot discard
		// existing extension fields. Arrays have explicit complete native records.
		if _, nested := previsFields[rule]; nested {
			item, ok := value.(map[string]any)
			if !ok {
				return BadAuthRequest("预演嵌套字段无效")
			}
			current, _ := target[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
			}
			if err := p.patchPrevisEntity(current, item, rule); err != nil {
				return err
			}
			target[key] = current
		} else {
			if err := p.validatePrevisValue(value, rule); err != nil {
				return err
			}
			target[key] = value
		}
	}
	return validatePrevisRequired(target, kind)
}

func (p *planner) editNativePrevis(doc Document, op Operation, ctx ValidationContext) error {
	if op.Type == "edit_previs_scene" && op.Action == "reorder" && op.ID == text(doc["id"]) && len(op.PrevisOperations) == 0 && len(op.PrevisEntityOperations) == 1 && op.PrevisEntityOperations[0].Type == "reorder_scenes" {
		ordered, err := p.canvasReorderItems(maps(doc["previsScenes"]), op.PrevisEntityOperations[0].IDs)
		if err != nil {
			return err
		}
		doc["previsScenes"] = p.mapsAsAny(ordered)
		return nil
	}
	if op.Type != "edit_previs_scene" || len(op.PrevisOperations) > 0 || len(op.PrevisEntityOperations) < 1 || len(op.PrevisEntityOperations) > 10000 {
		return BadAuthRequest("原生预演操作无效")
	}
	scenes := maps(doc["previsScenes"])
	index := p.canvasNodeIndex(scenes, op.ID)
	if op.Action == "initialize" {
		if index >= 0 || len(scenes) >= canvasPrevisMaxScenes {
			return BadAuthRequest("预演场景重复或超限")
		}
		scenes = append(scenes, map[string]any{"id": op.ID, "version": float64(1), "title": "未命名场景", "background": "#111827", "environmentIntensity": float64(1), "gridVisible": true, "objects": []any{}, "cameras": []any{}, "lights": []any{}, "shots": []any{}, "createdAt": p.now().UTC().Format(time.RFC3339Nano)})
		index = len(scenes) - 1
	} else if op.Action != "" {
		return BadAuthRequest("预演场景动作无效")
	}
	if index < 0 {
		return failure("entity_deleted", "预演场景不存在")
	}
	scene := scenes[index]
	for _, change := range op.PrevisEntityOperations {
		if change.Type == "settings" {
			if err := p.patchPrevisEntity(scene, change.Patch, "settings"); err != nil {
				return err
			}
			continue
		}
		parts := strings.SplitN(change.Type, "_", 2)
		if len(parts) != 2 {
			return BadAuthRequest("预演实体操作无效")
		}
		action, kind := parts[0], parts[1]
		if kind != "object" && kind != "camera" && kind != "light" && kind != "shot" {
			return BadAuthRequest("预演实体类型无效")
		}
		key := kind + "s"
		items := maps(scene[key])
		at := p.canvasNodeIndex(items, change.ID)
		if action == "reorder" {
			ordered, err := p.canvasReorderItems(items, change.IDs)
			if err != nil {
				return err
			}
			scene[key] = p.mapsAsAny(ordered)
			continue
		}
		if err := validateCanvasID(change.ID, "预演实体 ID", 80); err != nil {
			return err
		}
		switch action {
		case "upsert":
			if at < 0 {
				items = append(items, map[string]any{"id": change.ID})
				at = len(items) - 1
			}
			if err := p.patchPrevisEntity(items[at], change.Patch, kind); err != nil {
				return err
			}
		case "delete":
			if at < 0 {
				return failure("entity_deleted", "预演实体不存在")
			}
			items = append(items[:at:at], items[at+1:]...)
		default:
			return BadAuthRequest("预演实体操作无效")
		}
		scene[key] = p.mapsAsAny(items)
	}
	if err := p.canvasPrevisValidateScene(scene); err != nil {
		return err
	}
	if err := p.validateNativePrevisReferences(doc, scene, ctx); err != nil {
		return err
	}
	scene["updatedAt"] = p.now().UTC().Format(time.RFC3339Nano)
	if err := p.canvasPrevisScenesSize(scenes); err != nil {
		return err
	}
	doc["previsScenes"] = scenes
	return nil
}

func (p *planner) validateNativePrevisReferences(doc Document, scene map[string]any, ctx ValidationContext) error {
	var referenceErr error
	walkNodeReferences(scene, func(id string) {
		if id != "" && entity(doc, "node:"+id) == nil {
			referenceErr = BadAuthRequest("预演引用的画布节点不存在")
		}
	})
	if referenceErr != nil {
		return referenceErr
	}
	if env, ok := scene["environment"].(map[string]any); ok && text(env["mode"]) == "panorama" && assets.ResourceID(text(env["storageKey"])) == "" && assets.ResourceID(text(env["url"])) == "" {
		return failure("resource_not_owned", "全景环境需要已上传的图片资源")
	}
	for _, camera := range maps(scene["cameras"]) {
		if p.numberValue(camera["far"], 500) <= p.numberValue(camera["near"], 0.05) {
			return BadAuthRequest("摄影机远裁剪面必须大于近裁剪面")
		}
	}
	for _, object := range maps(scene["objects"]) {
		if text(object["url"]) == previsDefaultActorURL && (text(object["kind"]) != "actor" || text(object["assetId"]) != "") {
			return BadAuthRequest("默认演员地址只用于内置演员")
		}
		if binding, ok := object["characterBinding"].(map[string]any); ok {
			a, v, n := text(binding["characterAssetId"]), text(binding["characterVersionId"]), text(binding["referenceNodeId"])
			if err := p.canvasPrevisValidateCharacterBindingInCanvas(doc, PrevisOperation{CharacterAssetID: &a, CharacterVersionID: &v, ReferenceNodeID: &n}); err != nil {
				return err
			}
		}
		if id := text(object["activeMotionClipId"]); id != "" && p.canvasNodeIndex(maps(object["motionClips"]), id) < 0 {
			return BadAuthRequest("当前动作片段不存在")
		}
		if id := text(object["assetId"]); id != "" {
			if ctx.Asset == nil {
				return failure("resource_not_owned", "预演素材未授权")
			}
			asset, err := ctx.Asset(id)
			if err != nil {
				return err
			}
			kind := text(object["kind"])
			expected := "model"
			if kind == "billboard" {
				expected = "image"
			}
			if asset.ID != id || asset.Status == "archived" || asset.Kind != expected {
				return failure("resource_not_owned", "预演素材类型或归属无效")
			}
			for _, key := range []string{"storageKey", "url"} {
				if resource := assets.ResourceID(text(object[key])); resource != "" && resource != asset.ResourceID {
					return failure("resource_not_owned", "预演素材资源不匹配")
				}
			}
		}
	}
	return nil
}
