package app

import (
	"fmt"
)

func cloudAgentPrevisActorTravelOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, objects []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	object := cloudAgentPrevisFindByID(objects, operation.ID)
	if object == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标演员不存在")
	}
	summary, err := cloudAgentPrevisApplyActorTravel(scene, object, operation)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	scene["objects"] = objects
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: truncateRunes(stringValue(object["name"]), cloudAgentPrevisTextLimit), Summary: summary}, nil
}

func cloudAgentPrevisArrayAddOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, objects []map[string]any, index int) (cloudAgentApprovalPreviewItem, error) {
	if err := validateCloudAgentID(operation.ID, fmt.Sprintf("operations[%d].id", index), 72); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	item, err := cloudAgentPrevisApplyArrayAdd(&objects, operation, index)
	if err == nil {
		scene["objects"] = objects
	}
	return item, err
}

func cloudAgentPrevisObjectPatchOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, objects []map[string]any, index int) (cloudAgentApprovalPreviewItem, error) {
	item, err := cloudAgentPrevisApplyObjectPatch(&objects, operation, index)
	if err == nil {
		scene["objects"] = objects
	}
	return item, err
}

func cloudAgentPrevisActorMotionOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, objects []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	object := cloudAgentPrevisFindByID(objects, operation.ID)
	if object == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标对象不存在")
	}
	if err := cloudAgentPrevisApplyActorMotion(object, operation, cloudAgentPrevisPerformanceDuration(scene)); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	scene["objects"] = objects
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(object["name"]), Summary: fmt.Sprintf("修改演员《%s》的动作", stringValue(object["name"]))}, nil
}

func cloudAgentPrevisMotionPathOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, objects, cameras []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	// 路径可以给演员、道具，也可以给摄影机（运镜路径）。
	item := cloudAgentPrevisFindByID(objects, operation.ID)
	locked := item != nil && (stringValue(item["kind"]) == "actor" || stringValue(item["primitive"]) == "character" || stringValue(item["kind"]) == "layout")
	isCamera := false
	if item == nil {
		item = cloudAgentPrevisFindByID(cameras, operation.ID)
		isCamera = item != nil
	}
	if item == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标对象或摄影机不存在")
	}
	summary := fmt.Sprintf("清除《%s》的路径动画", stringValue(item["name"]))
	if operation.Type == "object_motion_path_set" {
		duration := cloudAgentPrevisPerformanceDuration(scene)
		if isCamera {
			duration = cloudAgentPrevisCameraDuration(scene, operation.ID)
		}
		var err error
		if summary, err = cloudAgentPrevisSetMotionPath(scene, item, operation, duration, locked); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
	} else {
		delete(item, "motionPath")
		item["keyframes"] = []map[string]any{}
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(item["name"]), Summary: summary}, nil
}

func cloudAgentPrevisObjectKeyframeOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, objects []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	object := cloudAgentPrevisFindByID(objects, operation.ID)
	if object == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标对象不存在")
	}
	duration := cloudAgentPrevisPerformanceDuration(scene)
	var err error
	if operation.Type == "object_keyframe_upsert" {
		err = cloudAgentPrevisUpsertKeyframe(object, operation, duration)
	} else {
		err = cloudAgentPrevisRemoveKeyframe(object, operation)
	}
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	scene["objects"] = objects
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(object["name"]), Summary: fmt.Sprintf("修改对象《%s》的动画", stringValue(object["name"]))}, nil
}
