package app

import (
	"fmt"
	"math"
	"strings"
	"yingce/backend/internal/repository"
)

func cloudAgentPrevisMapTransform(item map[string]any) map[string]any {
	if transform, ok := item["transform"].(map[string]any); ok {
		return transform
	}
	transform := map[string]any{"position": []any{0, 0, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
	item["transform"] = transform
	return transform
}

func cloudAgentPrevisApplyTransform(item map[string]any, operation cloudAgentPrevisPatchOperation) error {
	if err := cloudAgentPrevisOptionalVec3(operation.Position, "position"); err != nil {
		return err
	}
	if err := cloudAgentPrevisOptionalVec3(operation.Rotation, "rotation"); err != nil {
		return err
	}
	if err := cloudAgentPrevisOptionalVec3(operation.Scale, "scale"); err != nil {
		return err
	}
	transform := cloudAgentPrevisMapTransform(item)
	if operation.Position != nil {
		transform["position"] = cloudAgentPrevisVec3(*operation.Position)
	}
	if operation.Rotation != nil {
		transform["rotation"] = cloudAgentPrevisVec3(*operation.Rotation)
	}
	if operation.Scale != nil {
		transform["scale"] = cloudAgentPrevisVec3(*operation.Scale)
	}
	return nil
}

// cloudAgentPrevisGroundScene mirrors web/src/lib/canvas/previs/previs-grounding.ts:
// objects may be raised above the ground, but never sink below it. Actors keep
// their foot height so an existing scene on a step or platform survives an
// Agent edit. Layout objects stay exactly on y=0 because their local geometry is
// already grounded; raising a whole city would make it float.
func cloudAgentPrevisGroundScene(scene map[string]any) {
	for _, object := range creationMaps(scene["objects"]) {
		locked := stringValue(object["kind"]) == "layout"
		actor := stringValue(object["kind"]) == "actor" || stringValue(object["primitive"]) == "character"
		if transform, ok := object["transform"].(map[string]any); ok {
			cloudAgentPrevisGroundPosition(transform, locked)
		}
		for _, keyframe := range creationMaps(object["keyframes"]) {
			if transform, ok := keyframe["transform"].(map[string]any); ok {
				cloudAgentPrevisGroundPosition(transform, locked)
			}
		}
		if !locked && !actor {
			continue
		}
		if path, ok := object["motionPath"].(map[string]any); ok {
			if points, ok := path["points"].([]any); ok {
				for _, point := range points {
					if vec, ok := point.([]any); ok && len(vec) == 3 {
						// 路径点的 Y 是离地高度（演员悬空移动时写进关键帧 lift），只把地下的点拉回 0。
						if locked || !(numberValue(vec[1], math.NaN()) >= 0) {
							vec[1] = 0.0
						}
					}
				}
			}
		}
	}
}

func cloudAgentPrevisGroundPosition(transform map[string]any, locked bool) {
	position, ok := transform["position"].([]any)
	if !ok || len(position) != 3 {
		return
	}
	y := numberValue(position[1], 0)
	if locked || math.IsNaN(y) || math.IsInf(y, 0) || y < -0.005 {
		position[1] = 0.0
	}
}

func cloudAgentPrevisGroundVec3(position []any, locked bool) {
	if len(position) != 3 {
		return
	}
	y := numberValue(position[1], 0)
	if locked || math.IsNaN(y) || math.IsInf(y, 0) || y < -0.005 {
		position[1] = 0.0
	}
}

func cloudAgentPrevisApplyName(item map[string]any, value *string, label string) error {
	if value == nil {
		return nil
	}
	if err := cloudAgentPrevisValidateText(*value, label, cloudAgentPrevisTextLimit, true); err != nil {
		return err
	}
	item["name"] = *value
	return nil
}

// cloudAgentPrevisCharacterBindingHint 告诉模型三个绑定字段从哪里取值；所有绑定错误都带上它。
const cloudAgentPrevisCharacterBindingHint = "角色绑定的三个字段必须同时提供：先用 canvas_get_state 读取角色卡节点，characterAssetId 取 character.assetId，characterVersionId 取 character.versionId，referenceNodeId 取该角色卡节点 id"

func cloudAgentPrevisCharacterBindingError(field, issue, problem string) error {
	return cloudAgentFieldError(field, issue, problem+"。"+cloudAgentPrevisCharacterBindingHint)
}

func cloudAgentPrevisCharacterBindingProvided(operation cloudAgentPrevisPatchOperation) bool {
	return operation.CharacterAssetID != nil || operation.CharacterVersionID != nil || operation.ReferenceNodeID != nil || operation.CharacterName != nil
}

// cloudAgentPrevisCharacterBindingFieldsError 按固定顺序检查三个绑定字段，报告第一个缺失或格式错误的字段。
func cloudAgentPrevisCharacterBindingFieldsError(operation cloudAgentPrevisPatchOperation) error {
	for _, field := range []struct {
		value *string
		label string
	}{
		{operation.CharacterAssetID, "characterAssetId"},
		{operation.CharacterVersionID, "characterVersionId"},
		{operation.ReferenceNodeID, "referenceNodeId"},
	} {
		if field.value == nil || strings.TrimSpace(*field.value) == "" {
			return cloudAgentPrevisCharacterBindingError(field.label, "required", "角色绑定缺少 "+field.label)
		}
		if err := validateCloudAgentID(*field.value, field.label, 120); err != nil {
			return cloudAgentPrevisCharacterBindingError(field.label, "invalid", cloudAgentSafeToolError(err))
		}
	}
	return nil
}

func cloudAgentPrevisApplyCharacterBinding(item map[string]any, operation cloudAgentPrevisPatchOperation) error {
	if !cloudAgentPrevisCharacterBindingProvided(operation) {
		return nil
	}
	if err := cloudAgentPrevisCharacterBindingFieldsError(operation); err != nil {
		return err
	}
	binding := map[string]any{
		"characterAssetId":   *operation.CharacterAssetID,
		"characterVersionId": *operation.CharacterVersionID,
		"referenceNodeId":    *operation.ReferenceNodeID,
	}
	if operation.CharacterName != nil {
		if err := cloudAgentPrevisValidateText(*operation.CharacterName, "characterName", cloudAgentPrevisTextLimit, true); err != nil {
			return err
		}
		binding["characterName"] = *operation.CharacterName
	}
	item["characterBinding"] = binding
	item["sourceNodeId"] = *operation.ReferenceNodeID
	return nil
}

// cloudAgentPrevisValidateCharacterBindingInCanvas 要求 referenceNodeId 是匹配的角色卡。版本接受
// 节点记录的 characterVersionId，或 canvas_get_state 对该角色卡返回的 character.versionId
// （current 策略下是资产当前主版本），与读取口径一致，不接受其他版本。
func cloudAgentPrevisValidateCharacterBindingInCanvas(repo *repository.Repository, userID string, doc map[string]any, operation cloudAgentPrevisPatchOperation) error {
	if !cloudAgentPrevisCharacterBindingProvided(operation) {
		return nil
	}
	if err := cloudAgentPrevisCharacterBindingFieldsError(operation); err != nil {
		return err
	}
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) != *operation.ReferenceNodeID {
			continue
		}
		metadata, _ := node["metadata"].(map[string]any)
		if stringValue(metadata["workflowKind"]) != "character" {
			return cloudAgentPrevisCharacterBindingError("referenceNodeId", "mismatch", "referenceNodeId 不是角色卡节点")
		}
		if stringValue(metadata["characterAssetId"]) != *operation.CharacterAssetID {
			return cloudAgentPrevisCharacterBindingError("characterAssetId", "mismatch", "characterAssetId 与该角色卡关联的角色资产不一致")
		}
		if stringValue(metadata["characterVersionId"]) == *operation.CharacterVersionID {
			return nil
		}
		if repo != nil {
			if character, err := cloudAgentResolveCharacter(repo, userID, "", node); err == nil && character.Card.VersionID == *operation.CharacterVersionID {
				return nil
			}
		}
		return cloudAgentPrevisCharacterBindingError("characterVersionId", "mismatch", "characterVersionId 与该角色卡当前读取到的版本不一致")
	}
	return cloudAgentPrevisCharacterBindingError("referenceNodeId", "mismatch", "referenceNodeId 不属于当前画布")
}

func cloudAgentPrevisShotDuration(scene map[string]any) float64 {
	for _, shot := range creationMaps(scene["shots"]) {
		if stringValue(shot["id"]) == stringValue(scene["activeShotId"]) {
			return numberValue(shot["duration"], 5)
		}
	}
	return 5
}

// cloudAgentPrevisPerformanceDuration 是共享表演时间线：所有镜头都从 0 秒播放同一段表演，
// 因此动作、走位与对象关键帧可以写到最长镜头的结尾，短镜头只播放其中一段。
func cloudAgentPrevisPerformanceDuration(scene map[string]any) float64 {
	longest := 0.0
	for _, shot := range creationMaps(scene["shots"]) {
		if duration := numberValue(shot["duration"], 0); duration > longest {
			longest = duration
		}
	}
	if longest > 0 {
		return longest
	}
	return 5
}

// cloudAgentPrevisCameraDuration 是某台摄影机可用的最长镜头时长。
// 同一台摄影机被多个镜头引用时取最大时长；没有镜头引用时回退到活动镜头。
func cloudAgentPrevisCameraDuration(scene map[string]any, cameraID string) float64 {
	longest := 0.0
	for _, shot := range creationMaps(scene["shots"]) {
		if stringValue(shot["cameraId"]) != cameraID {
			continue
		}
		if duration := numberValue(shot["duration"], 0); duration > longest {
			longest = duration
		}
	}
	if longest > 0 {
		return longest
	}
	return cloudAgentPrevisShotDuration(scene)
}

func cloudAgentPrevisKeyframeTransform(item map[string]any, operation cloudAgentPrevisPatchOperation) (map[string]any, error) {
	transform := cloudAgentPrevisMapTransform(item)
	result := map[string]any{}
	for _, key := range []string{"position", "rotation", "scale"} {
		value, ok := cloudAgentPrevisNumberVec3(transform[key])
		if !ok {
			return nil, BadAuthRequest("关键帧目标对象 transform 无效")
		}
		result[key] = cloudAgentPrevisVec3(value)
	}
	for value, key := range map[*[]float64]string{operation.Position: "position", operation.Rotation: "rotation", operation.Scale: "scale"} {
		if value == nil {
			continue
		}
		if err := cloudAgentPrevisValidateVec3(*value, key); err != nil {
			return nil, err
		}
		result[key] = cloudAgentPrevisVec3(*value)
	}
	return result, nil
}

func cloudAgentPrevisValidateEasing(value string) error {
	if value == "" {
		return nil
	}
	return cloudAgentPrevisValidateEnum(value, "关键帧 easing", "step", "linear", "smooth")
}

func cloudAgentPrevisUpsertKeyframe(item map[string]any, operation cloudAgentPrevisPatchOperation, duration float64) error {
	if operation.Time == nil {
		return BadAuthRequest("关键帧操作需要 time")
	}
	if err := cloudAgentPrevisFiniteInRange(*operation.Time, "关键帧 time", 0, duration); err != nil {
		return err
	}
	if err := cloudAgentPrevisValidateEasing(operation.Easing); err != nil {
		return err
	}
	transform, err := cloudAgentPrevisKeyframeTransform(item, operation)
	if err != nil {
		return err
	}
	keyframes := creationMaps(item["keyframes"])
	keyframeID := ""
	if operation.KeyframeID != nil {
		if err := validateCloudAgentID(*operation.KeyframeID, "keyframeId", 120); err != nil {
			return err
		}
		keyframeID = *operation.KeyframeID
	}
	match := -1
	for index, keyframe := range keyframes {
		if (keyframeID != "" && stringValue(keyframe["id"]) == keyframeID) || math.Abs(numberValue(keyframe["time"], math.NaN())-*operation.Time) < 0.001 {
			match = index
			break
		}
	}
	if match < 0 {
		if keyframeID == "" {
			keyframeID = fmt.Sprintf("%s-kf-%d", stringValue(item["id"]), len(keyframes)+1)
		}
		keyframe := map[string]any{"id": keyframeID, "time": *operation.Time, "transform": transform}
		if operation.Easing != "" {
			keyframe["easing"] = operation.Easing
		}
		keyframes = append(keyframes, keyframe)
	} else {
		keyframes[match]["id"] = keyframeIDOrExisting(keyframeID, keyframes[match])
		keyframes[match]["time"] = *operation.Time
		keyframes[match]["transform"] = transform
		if operation.Easing == "" {
			delete(keyframes[match], "easing")
		} else {
			keyframes[match]["easing"] = operation.Easing
		}
	}
	for i := range keyframes {
		for j := i + 1; j < len(keyframes); j++ {
			if numberValue(keyframes[j]["time"], 0) < numberValue(keyframes[i]["time"], 0) {
				keyframes[i], keyframes[j] = keyframes[j], keyframes[i]
			}
		}
	}
	item["keyframes"] = keyframes
	return nil
}

func keyframeIDOrExisting(requested string, existing map[string]any) string {
	if requested != "" {
		return requested
	}
	return stringValue(existing["id"])
}

func cloudAgentPrevisRemoveKeyframe(item map[string]any, operation cloudAgentPrevisPatchOperation) error {
	if operation.KeyframeID == nil || strings.TrimSpace(*operation.KeyframeID) == "" {
		return BadAuthRequest("删除关键帧需要 keyframeId")
	}
	if err := validateCloudAgentID(*operation.KeyframeID, "keyframeId", 120); err != nil {
		return err
	}
	keyframes := creationMaps(item["keyframes"])
	filtered := make([]map[string]any, 0, len(keyframes))
	for _, keyframe := range keyframes {
		if stringValue(keyframe["id"]) != *operation.KeyframeID {
			filtered = append(filtered, keyframe)
		}
	}
	if len(filtered) == len(keyframes) {
		return BadAuthRequest("目标关键帧不存在")
	}
	item["keyframes"] = filtered
	return nil
}
