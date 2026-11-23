package app

import (
	"fmt"
	"math"
)

func cloudAgentPrevisValidateScene(scene map[string]any) error {
	if err := cloudAgentPrevisValidateSceneHeader(scene); err != nil {
		return err
	}
	objectByID, cameraByID, shotByID, err := cloudAgentPrevisSceneIDs(scene)
	if err != nil {
		return err
	}
	for id, object := range objectByID {
		if err := cloudAgentPrevisValidateObjectAppearance(id, object); err != nil {
			return err
		}
		if err := cloudAgentPrevisValidateObjectAnimation(object); err != nil {
			return err
		}
		if err := cloudAgentPrevisValidateObjectParent(id, object, objectByID); err != nil {
			return err
		}
	}
	for id, camera := range cameraByID {
		if err := cloudAgentPrevisValidateCamera(id, camera); err != nil {
			return err
		}
	}
	for id, shot := range shotByID {
		if err := cloudAgentPrevisValidateShot(id, shot, cameraByID); err != nil {
			return err
		}
	}
	for _, light := range creationMaps(scene["lights"]) {
		if err := cloudAgentPrevisValidateLight(light); err != nil {
			return err
		}
	}
	if err := cloudAgentPrevisValidateParentCycles(objectByID); err != nil {
		return err
	}
	return nil
}

func cloudAgentPrevisValidateSceneHeader(scene map[string]any) error {
	if err := validateCloudAgentID(stringValue(scene["id"]), "导演场景 ID", 80); err != nil {
		return err
	}
	if numberValue(scene["version"], 0) != 1 {
		return BadAuthRequest("预演台场景版本不受支持")
	}
	if err := cloudAgentPrevisValidateText(stringValue(scene["title"]), "场景标题", cloudAgentPrevisTextLimit, true); err != nil {
		return err
	}
	background := cloudAgentPrevisNormalizeSceneColor(stringValue(scene["background"]), "#111827")
	scene["background"] = background
	if err := cloudAgentPrevisValidateColor(stringValue(scene["background"]), "场景背景色"); err != nil {
		return err
	}
	if err := cloudAgentPrevisFiniteInRange(numberValue(scene["environmentIntensity"], math.NaN()), "环境光强度", 0, 100); err != nil {
		return err
	}
	if world, ok := scene["world"].(map[string]any); ok {
		if err := cloudAgentPrevisValidateWorld(world); err != nil {
			return err
		}
	}
	if err := cloudAgentPrevisValidateMarkersAndTravels(scene); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateSketchPaths(scene); err != nil {
		return err
	}
	return nil
}

func cloudAgentPrevisSceneIDs(scene map[string]any) (objectByID, cameraByID, shotByID map[string]map[string]any, err error) {
	objects := creationMaps(scene["objects"])
	cameras := creationMaps(scene["cameras"])
	lights := creationMaps(scene["lights"])
	shots := creationMaps(scene["shots"])
	if len(objects) > cloudAgentPrevisMaxObjects || len(cameras) > cloudAgentPrevisMaxCameras || len(lights) > cloudAgentPrevisMaxLights || len(shots) > cloudAgentPrevisMaxShots {
		return nil, nil, nil, BadAuthRequest("预演台场景对象、摄影机、灯光或镜头数量超过限制")
	}
	objectByID, err = cloudAgentPrevisIDSet(objects, "导演对象 ID")
	if err != nil {
		return nil, nil, nil, err
	}
	cameraByID, err = cloudAgentPrevisIDSet(cameras, "导演摄影机 ID")
	if err != nil {
		return nil, nil, nil, err
	}
	_, err = cloudAgentPrevisIDSet(lights, "导演灯光 ID")
	if err != nil {
		return nil, nil, nil, err
	}
	shotByID, err = cloudAgentPrevisIDSet(shots, "导演镜头 ID")
	if err != nil {
		return nil, nil, nil, err
	}
	if len(shots) == 0 || stringValue(scene["activeShotId"]) == "" || shotByID[stringValue(scene["activeShotId"])] == nil {
		return nil, nil, nil, BadAuthRequest("预演台场景必须包含有效的 activeShotId")
	}
	return objectByID, cameraByID, shotByID, nil
}

func cloudAgentPrevisValidateObjectAppearance(id string, object map[string]any) error {
	if err := cloudAgentPrevisValidateText(stringValue(object["name"]), "导演对象名称", cloudAgentPrevisTextLimit, true); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateEnum(stringValue(object["kind"]), "导演对象类型", "primitive", "model", "actor", "billboard", "prop", "layout"); err != nil {
		// 整场景校验：任何一个对象不合法都会拒绝整次补丁，必须指明是哪个对象，否则 Agent 会误以为是自己要改的对象有问题。
		return BadAuthRequest(fmt.Sprintf("场景中的对象 %s（%s）类型 %q 不受当前服务支持；这不是本次操作对象的问题，请把错误原样告知用户（可能需要更新后端）", id, truncateRunes(stringValue(object["name"]), 40), stringValue(object["kind"])))
	}
	if stringValue(object["kind"]) == "prop" {
		if _, ok := cloudAgentPrevisFindProp(stringValue(object["propId"])); !ok {
			return BadAuthRequest("预演台道具 " + id + " 的 propId 不在目录中")
		}
		if params, ok := object["params"].(map[string]any); ok {
			if _, err := cloudAgentPrevisNormalizeParams(stringValue(object["propId"]), nil, params); err != nil {
				return err
			}
		}
	}
	if stringValue(object["kind"]) == "layout" {
		layout, _ := object["layout"].(map[string]any)
		if layout == nil {
			return BadAuthRequest("预演台布景 " + id + " 缺少 layout")
		}
		if err := cloudAgentPrevisValidateLayout(layout); err != nil {
			return err
		}
	}
	if primitive := stringValue(object["primitive"]); primitive != "" {
		if err := cloudAgentPrevisValidateEnum(primitive, "导演对象 primitive", "box", "sphere", "cylinder", "plane", "character"); err != nil {
			return err
		}
	}
	objectColor := cloudAgentPrevisNormalizeSceneColor(stringValue(object["color"]), "#8795a5")
	object["color"] = objectColor
	if err := cloudAgentPrevisValidateColor(stringValue(object["color"]), "导演对象颜色"); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateTransform(object["transform"], "导演对象 "+id); err != nil {
		return err
	}
	return nil
}

func cloudAgentPrevisValidateObjectAnimation(object map[string]any) error {
	if binding, ok := object["characterBinding"].(map[string]any); ok {
		for _, key := range []string{"characterAssetId", "characterVersionId", "referenceNodeId"} {
			if err := validateCloudAgentID(stringValue(binding[key]), "角色绑定 "+key, 120); err != nil {
				return err
			}
		}
		if name := stringValue(binding["characterName"]); name != "" {
			if err := cloudAgentPrevisValidateText(name, "角色绑定 characterName", cloudAgentPrevisTextLimit, true); err != nil {
				return err
			}
		}
	}
	if keyframes, ok := object["keyframes"]; ok {
		for _, keyframe := range creationMaps(keyframes) {
			if err := validateCloudAgentID(stringValue(keyframe["id"]), "对象关键帧 ID", 120); err != nil {
				return err
			}
			if err := cloudAgentPrevisFiniteInRange(numberValue(keyframe["time"], math.NaN()), "对象关键帧 time", 0, 60); err != nil {
				return err
			}
			if err := cloudAgentPrevisValidateTransform(keyframe["transform"], "对象关键帧 "+stringValue(keyframe["id"])); err != nil {
				return err
			}
			if lift, ok := keyframe["lift"]; ok && lift != nil {
				if err := cloudAgentPrevisFiniteInRange(numberValue(lift, math.NaN()), "对象关键帧 lift", 0, 50); err != nil {
					return err
				}
			}
		}
	}
	if motionPath, ok := object["motionPath"].(map[string]any); ok {
		if err := cloudAgentPrevisValidateMotionPath(motionPath); err != nil {
			return err
		}
	}
	return nil
}

func cloudAgentPrevisValidateObjectParent(id string, object map[string]any, objectByID map[string]map[string]any) error {
	if parentID := stringValue(object["parentId"]); parentID != "" {
		if err := validateCloudAgentID(parentID, "导演对象 parentId", 80); err != nil {
			return err
		}
		if objectByID[parentID] == nil || parentID == id {
			return BadAuthRequest("预演台对象 parentId 无效")
		}
	}
	return nil
}

func cloudAgentPrevisValidateCamera(id string, camera map[string]any) error {
	if err := cloudAgentPrevisValidateText(stringValue(camera["name"]), "导演摄影机名称", cloudAgentPrevisTextLimit, true); err != nil {
		return err
	}
	if err := cloudAgentPrevisFiniteInRange(numberValue(camera["focalLength"], math.NaN()), "摄影机焦段", 1, 300); err != nil {
		return err
	}
	target, targetOK := cloudAgentPrevisNumberVec3(camera["target"])
	if !targetOK {
		return BadAuthRequest("导演摄影机 " + id + " target 必须包含3个数字")
	}
	if err := cloudAgentPrevisValidateVec3(target, "导演摄影机 "+id+" target"); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateTransform(camera["transform"], "导演摄影机 "+id); err != nil {
		return err
	}
	if keyframes, ok := camera["keyframes"]; ok {
		for _, keyframe := range creationMaps(keyframes) {
			if err := validateCloudAgentID(stringValue(keyframe["id"]), "摄影机关键帧 ID", 120); err != nil {
				return err
			}
			if err := cloudAgentPrevisFiniteInRange(numberValue(keyframe["time"], math.NaN()), "摄影机关键帧 time", 0, 60); err != nil {
				return err
			}
			if err := cloudAgentPrevisValidateTransform(keyframe["transform"], "摄影机关键帧 "+stringValue(keyframe["id"])); err != nil {
				return err
			}
		}
	}
	if err := cloudAgentPrevisValidateCameraMoves(id, camera); err != nil {
		return err
	}
	if motionPath, ok := camera["motionPath"].(map[string]any); ok {
		if err := cloudAgentPrevisValidateMotionPath(motionPath); err != nil {
			return err
		}
	}
	return nil
}

func cloudAgentPrevisValidateShot(id string, shot map[string]any, cameraByID map[string]map[string]any) error {
	cameraID := stringValue(shot["cameraId"])
	if cameraByID[cameraID] == nil {
		return BadAuthRequest("导演镜头 " + id + " 引用了不存在的摄影机")
	}
	duration := numberValue(shot["duration"], math.NaN())
	fps := numberValue(shot["fps"], math.NaN())
	if err := cloudAgentPrevisFiniteInRange(duration, "导演镜头时长", 0.1, 60); err != nil {
		return err
	}
	if err := cloudAgentPrevisFiniteInRange(fps, "导演镜头帧率", 1, 60); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateText(stringValue(shot["name"]), "导演镜头名称", cloudAgentPrevisTextLimit, true); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateEnum(stringValue(shot["shotSize"]), "导演景别", "extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateEnum(stringValue(shot["cameraMove"]), "导演运镜", "static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateText(stringValue(shot["prompt"]), "导演镜头提示词", 2000, false); err != nil {
		return err
	}
	if aspect, ok := shot["aspectRatio"]; ok && aspect != nil {
		if err := cloudAgentPrevisValidateEnum(stringValue(aspect), "导演镜头画幅", cloudAgentPrevisAspectRatios...); err != nil {
			return err
		}
	}
	return nil
}

func cloudAgentPrevisValidateLight(light map[string]any) error {
	lightID := stringValue(light["id"])
	if err := cloudAgentPrevisValidateText(stringValue(light["name"]), "导演灯光名称", cloudAgentPrevisTextLimit, true); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateEnum(stringValue(light["type"]), "导演灯光类型", "directional", "point", "spot", "ambient"); err != nil {
		return err
	}
	lightColor := cloudAgentPrevisNormalizeSceneColor(stringValue(light["color"]), "#ffffff")
	light["color"] = lightColor
	if err := cloudAgentPrevisValidateColor(stringValue(light["color"]), "导演灯光颜色"); err != nil {
		return err
	}
	if err := cloudAgentPrevisFiniteInRange(numberValue(light["intensity"], math.NaN()), "导演灯光强度", 0, 100); err != nil {
		return err
	}
	if angle, ok := light["angle"]; ok {
		if err := cloudAgentPrevisFiniteInRange(numberValue(angle, math.NaN()), "导演灯光 "+lightID+" angle", 0, 6.29); err != nil {
			return err
		}
	}
	if penumbra, ok := light["penumbra"]; ok {
		if err := cloudAgentPrevisFiniteInRange(numberValue(penumbra, math.NaN()), "导演灯光 "+lightID+" penumbra", 0, 1); err != nil {
			return err
		}
	}
	if err := cloudAgentPrevisValidateTransform(light["transform"], "导演灯光 "+lightID); err != nil {
		return err
	}
	return nil
}

func cloudAgentPrevisValidateParentCycles(objectByID map[string]map[string]any) error {
	// Parent links are a small directed graph. Walk every chain to reject cycles.
	for id := range objectByID {
		seen := map[string]bool{}
		for current := id; current != ""; {
			if seen[current] {
				return BadAuthRequest("预演台对象 parentId 不能形成环")
			}
			seen[current] = true
			current = stringValue(objectByID[current]["parentId"])
			if current != "" && objectByID[current] == nil {
				return BadAuthRequest("预演台对象 parentId 引用不存在对象")
			}
		}
	}
	return nil
}
