package app

import (
	"fmt"
	"math"
)

func cloudAgentPrevisCameraMoveOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, cameras []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	camera := cloudAgentPrevisFindByID(cameras, operation.ID)
	if camera == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标摄影机不存在；camera_move 的 id 是摄影机 ID")
	}
	summary, err := cloudAgentPrevisApplyCameraMove(scene, camera, operation)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: truncateRunes(stringValue(camera["name"]), cloudAgentPrevisTextLimit), Summary: summary}, nil
}

func cloudAgentPrevisCameraAddOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, cameras []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	if cloudAgentPrevisFindByID(cameras, operation.ID) != nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("新增摄影机 ID 已存在")
	}
	name := "摄影机"
	if operation.Name != nil {
		name = *operation.Name
	}
	if err := cloudAgentPrevisValidateText(name, "摄影机名称", cloudAgentPrevisTextLimit, true); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	camPos := []float64{4.8, 2.7, 6.8}
	camRot := []float64{0, 0, 0}
	camScale := []float64{1, 1, 1}
	camTarget := []float64{0, 1, 0}
	if operation.Position != nil {
		camPos = *operation.Position
	}
	if operation.Rotation != nil {
		camRot = *operation.Rotation
	}
	if operation.Scale != nil {
		camScale = *operation.Scale
	}
	if operation.Target != nil {
		camTarget = *operation.Target
	}
	if err := cloudAgentPrevisValidateVec3(camPos, "摄影机 position"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := cloudAgentPrevisValidateVec3(camTarget, "摄影机 target"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	focalLength := 35.0
	if operation.FocalLength != nil {
		focalLength = *operation.FocalLength
	}
	if err := cloudAgentPrevisFiniteInRange(focalLength, "摄影机焦段", 1, 300); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	fov := 2 * math.Atan(36.0/(2*math.Max(1, focalLength))) * 180 / math.Pi
	newCamera := map[string]any{
		"id": operation.ID, "name": name,
		"transform": map[string]any{"position": cloudAgentPrevisVec3(camPos), "rotation": cloudAgentPrevisVec3(camRot), "scale": cloudAgentPrevisVec3(camScale)},
		"target":    cloudAgentPrevisVec3(camTarget), "focalLength": focalLength, "fov": fov,
		"aperture": 2.8, "focusDistance": 5.0, "near": 0.05, "far": 500.0, "keyframes": []any{},
	}
	scene["cameras"] = append(cameras, newCamera)
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: name, Summary: fmt.Sprintf("新增摄影机《%s》", name)}, nil
}

func cloudAgentPrevisCameraKeyframeOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, cameras []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	camera := cloudAgentPrevisFindByID(cameras, operation.ID)
	if camera == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标摄影机不存在")
	}
	duration := cloudAgentPrevisCameraDuration(scene, operation.ID)
	var err error
	if operation.Type == "camera_keyframe_upsert" {
		err = cloudAgentPrevisUpsertKeyframe(camera, operation, duration)
	} else {
		err = cloudAgentPrevisRemoveKeyframe(camera, operation)
	}
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(camera["name"]), Summary: fmt.Sprintf("修改摄影机《%s》的动画", stringValue(camera["name"]))}, nil
}

func cloudAgentPrevisCameraUpdateOrRemoveOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, cameras, shots []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	camera := cloudAgentPrevisFindByID(cameras, operation.ID)
	if camera == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标摄影机不存在")
	}
	if operation.Type == "camera_remove" {
		if len(cameras) <= 1 {
			return cloudAgentApprovalPreviewItem{}, BadAuthRequest("预演场景至少保留一台摄影机")
		}
		for _, shot := range shots {
			if stringValue(shot["cameraId"]) == operation.ID {
				return cloudAgentApprovalPreviewItem{}, BadAuthRequest("摄影机仍被镜头引用，不能删除")
			}
		}
		next := make([]map[string]any, 0, len(cameras)-1)
		for _, item := range cameras {
			if stringValue(item["id"]) != operation.ID {
				next = append(next, item)
			}
		}
		scene["cameras"] = next
		return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(camera["name"]), Summary: fmt.Sprintf("删除摄影机《%s》", stringValue(camera["name"]))}, nil
	}
	if err := cloudAgentPrevisApplyName(camera, operation.Name, "摄影机名称"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if operation.FocalLength != nil {
		if err := cloudAgentPrevisFiniteInRange(*operation.FocalLength, "摄影机焦段", 1, 300); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		camera["focalLength"] = *operation.FocalLength
	}
	if operation.Target != nil {
		if err := cloudAgentPrevisValidateVec3(*operation.Target, "target"); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		camera["target"] = cloudAgentPrevisVec3(*operation.Target)
	}
	if err := cloudAgentPrevisApplyTransform(camera, operation); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(camera["name"]), Summary: fmt.Sprintf("修改摄影机《%s》", stringValue(camera["name"]))}, nil
}
