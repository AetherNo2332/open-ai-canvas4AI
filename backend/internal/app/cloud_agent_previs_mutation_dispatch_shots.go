package app

import (
	"fmt"
	"strings"
)

func cloudAgentPrevisShotAddOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, cameras, shots []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	if cloudAgentPrevisFindByID(shots, operation.ID) != nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("新增镜头 ID 已存在")
	}
	cameraID := ""
	if operation.CameraID != nil {
		if err := validateCloudAgentID(*operation.CameraID, "摄影机 ID", 80); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		cameraID = *operation.CameraID
	} else if len(cameras) > 0 {
		cameraID = stringValue(cameras[0]["id"])
	}
	if cloudAgentPrevisFindByID(cameras, cameraID) == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("新增镜头必须引用现有摄影机")
	}
	name := "新镜头"
	if operation.Name != nil {
		name = *operation.Name
	}
	if err := cloudAgentPrevisValidateText(name, "镜头名称", cloudAgentPrevisTextLimit, true); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	duration, fps := 5.0, 24
	if operation.Duration != nil {
		duration = *operation.Duration
	}
	if operation.FPS != nil {
		fps = *operation.FPS
	}
	if err := cloudAgentPrevisFiniteInRange(duration, "镜头时长", 0.1, 60); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if fps < 1 || fps > 60 {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("镜头帧率超出限制")
	}
	shotSize, cameraMove := operation.ShotSize, operation.CameraMove
	if shotSize == "" {
		shotSize = "medium"
	}
	if cameraMove == "" {
		cameraMove = "static"
	}
	if err := cloudAgentPrevisValidateEnum(shotSize, "镜头景别", "extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := cloudAgentPrevisValidateEnum(cameraMove, "镜头运镜", "static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	shot := map[string]any{"id": operation.ID, "name": name, "cameraId": cameraID, "duration": duration, "fps": fps, "shotSize": shotSize, "cameraMove": cameraMove, "prompt": "", "cuts": []any{}}
	if err := cloudAgentPrevisNewShotOptions(shot, operation); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	scene["shots"] = append(shots, shot)
	if stringValue(scene["activeShotId"]) == "" {
		scene["activeShotId"] = operation.ID
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: name, Summary: fmt.Sprintf("新增镜头《%s》", name)}, nil
}

func cloudAgentPrevisNewShotOptions(shot map[string]any, operation cloudAgentPrevisPatchOperation) error {
	if operation.AspectRatio != "" {
		if err := cloudAgentPrevisValidateEnum(operation.AspectRatio, "镜头画幅 aspectRatio", cloudAgentPrevisAspectRatios...); err != nil {
			return err
		}
		shot["aspectRatio"] = operation.AspectRatio
	}
	if operation.Prompt != nil {
		if err := cloudAgentPrevisValidateText(*operation.Prompt, "镜头提示词", 2000, false); err != nil {
			return err
		}
		shot["prompt"] = *operation.Prompt
	}
	return nil
}

func cloudAgentPrevisShotUpdateOrRemoveOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, cameras, shots []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	shot := cloudAgentPrevisFindByID(shots, operation.ID)
	if shot == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标镜头不存在")
	}
	if operation.Type == "shot_remove" {
		if len(shots) <= 1 {
			return cloudAgentApprovalPreviewItem{}, BadAuthRequest("预演场景至少保留一个镜头")
		}
		next := make([]map[string]any, 0, len(shots)-1)
		for _, item := range shots {
			if stringValue(item["id"]) != operation.ID {
				next = append(next, item)
			}
		}
		scene["shots"] = next
		if stringValue(scene["activeShotId"]) == operation.ID {
			scene["activeShotId"] = stringValue(next[0]["id"])
		}
		return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(shot["name"]), Summary: fmt.Sprintf("删除镜头《%s》", stringValue(shot["name"]))}, nil
	}
	activeShotID, activeShotName := "", ""
	if operation.ActiveShotID != nil {
		activeShotID = strings.TrimSpace(*operation.ActiveShotID)
		if err := validateCloudAgentID(activeShotID, "activeShotId", 80); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		activeShot := cloudAgentPrevisFindByID(shots, activeShotID)
		if activeShot == nil {
			return cloudAgentApprovalPreviewItem{}, BadAuthRequest("activeShotId 指向的镜头不存在")
		}
		activeShotName = stringValue(activeShot["name"])
	}
	if err := cloudAgentPrevisApplyName(shot, operation.Name, "镜头名称"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := cloudAgentPrevisUpdateShotFields(shot, operation, cameras); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	summary := fmt.Sprintf("修改镜头《%s》", stringValue(shot["name"]))
	if activeShotID != "" {
		scene["activeShotId"] = activeShotID
		summary += fmt.Sprintf("，切到《%s》", activeShotName)
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(shot["name"]), Summary: summary}, nil
}

func cloudAgentPrevisUpdateShotFields(shot map[string]any, operation cloudAgentPrevisPatchOperation, cameras []map[string]any) error {
	if operation.CameraID != nil {
		if err := validateCloudAgentID(*operation.CameraID, "摄影机 ID", 80); err != nil {
			return err
		}
		if cloudAgentPrevisFindByID(cameras, *operation.CameraID) == nil {
			return BadAuthRequest("镜头引用的摄影机不存在")
		}
		shot["cameraId"] = *operation.CameraID
	}
	if operation.Duration != nil {
		if err := cloudAgentPrevisFiniteInRange(*operation.Duration, "镜头时长", 0.1, 60); err != nil {
			return err
		}
		shot["duration"] = *operation.Duration
	}
	if operation.FPS != nil {
		if *operation.FPS < 1 || *operation.FPS > 60 {
			return BadAuthRequest("镜头帧率超出限制")
		}
		shot["fps"] = *operation.FPS
	}
	if operation.ShotSize != "" {
		if err := cloudAgentPrevisValidateEnum(operation.ShotSize, "镜头景别", "extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"); err != nil {
			return err
		}
		shot["shotSize"] = operation.ShotSize
	}
	if operation.CameraMove != "" {
		if err := cloudAgentPrevisValidateEnum(operation.CameraMove, "镜头运镜", "static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"); err != nil {
			return err
		}
		shot["cameraMove"] = operation.CameraMove
	}
	if operation.AspectRatio != "" {
		if err := cloudAgentPrevisValidateEnum(operation.AspectRatio, "镜头画幅 aspectRatio", cloudAgentPrevisAspectRatios...); err != nil {
			return err
		}
		shot["aspectRatio"] = operation.AspectRatio
	}
	if operation.Prompt != nil {
		if err := cloudAgentPrevisValidateText(*operation.Prompt, "镜头提示词", 2000, false); err != nil {
			return err
		}
		shot["prompt"] = *operation.Prompt
	}
	return nil
}
