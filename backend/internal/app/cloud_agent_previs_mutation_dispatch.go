package app

import (
	"fmt"
)

func cloudAgentPrevisApplyPatchOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, index int) (cloudAgentApprovalPreviewItem, error) {
	if err := cloudAgentPrevisValidateText(operation.Type, fmt.Sprintf("operations[%d].type", index), 40, true); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := validateCloudAgentID(operation.ID, fmt.Sprintf("operations[%d].id", index), 80); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if (operation.Type == "scene_update" || operation.Type == "world_update") && operation.ID != stringValue(scene["id"]) {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest(operation.Type + " 的 id 必须是当前场景 ID")
	}
	return cloudAgentPrevisDispatchOperation(scene, operation, index)
}

func cloudAgentPrevisDispatchOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, index int) (cloudAgentApprovalPreviewItem, error) {
	objects, cameras, lights, shots := creationMaps(scene["objects"]), creationMaps(scene["cameras"]), creationMaps(scene["lights"]), creationMaps(scene["shots"])
	switch operation.Type {
	case "scene_update":
		return cloudAgentPrevisApplySceneUpdate(scene, operation)
	case "world_update":
		summary, err := cloudAgentPrevisApplyWorld(scene, operation)
		if err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: stringValue(scene["id"]), NodeTitle: stringValue(scene["title"]), Summary: summary}, nil
	case "actor_travel":
		return cloudAgentPrevisActorTravelOperation(scene, operation, objects)
	case "object_array_add":
		return cloudAgentPrevisArrayAddOperation(scene, operation, objects, index)
	case "object_add", "object_update", "object_remove":
		return cloudAgentPrevisObjectPatchOperation(scene, operation, objects, index)
	case "actor_motion_add", "actor_motion_remove":
		return cloudAgentPrevisActorMotionOperation(scene, operation, objects)
	case "object_motion_path_set", "object_motion_path_clear":
		return cloudAgentPrevisMotionPathOperation(scene, operation, objects, cameras)
	case "object_keyframe_upsert", "object_keyframe_remove":
		return cloudAgentPrevisObjectKeyframeOperation(scene, operation, objects)
	case "camera_move", "camera_move_remove":
		return cloudAgentPrevisCameraMoveOperation(scene, operation, cameras)
	case "shot_add":
		return cloudAgentPrevisShotAddOperation(scene, operation, cameras, shots)
	case "shot_update", "shot_remove":
		return cloudAgentPrevisShotUpdateOrRemoveOperation(scene, operation, cameras, shots)
	case "camera_keyframe_upsert", "camera_keyframe_remove":
		return cloudAgentPrevisCameraKeyframeOperation(scene, operation, cameras)
	case "camera_add":
		return cloudAgentPrevisCameraAddOperation(scene, operation, cameras)
	case "camera_update", "camera_remove":
		return cloudAgentPrevisCameraUpdateOrRemoveOperation(scene, operation, cameras, shots)
	case "light_add", "light_update", "light_remove":
		return cloudAgentPrevisLightOperation(scene, operation, lights)
	default:
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("不支持的预演台语义操作")
	}
}
