package app

import (
	"fmt"
	"math"
)

// cloudAgentPrevisMotionIDs 与前端 PREVIS_MOTIONS（previs-motion.ts）一一对应。
var cloudAgentPrevisMotionIDs = []string{"idle", "walk", "run", "talk", "wave", "nod", "shake_head", "look_around", "point", "phone_call", "sit_idle", "clap", "fight_idle", "punch", "jump", "fall", "cry", "cheer", "slap", "hold_hand", "hit_react"}

const cloudAgentPrevisMaxActorMotions = 32

// cloudAgentPrevisApplyActorMotion 在演员时间轴上插入/删除程序化动作片段。移动步态由位移自动推导，不需要写 walk/run。
func cloudAgentPrevisApplyActorMotion(object map[string]any, operation cloudAgentPrevisPatchOperation, duration float64) error {
	if stringValue(object["kind"]) != "actor" && stringValue(object["primitive"]) != "character" {
		return BadAuthRequest("只有演员可以添加动作")
	}
	motions := creationMaps(object["motions"])
	if operation.Type == "actor_motion_remove" {
		return cloudAgentPrevisRemoveActorMotion(object, motions, operation.KeyframeID)
	}
	if err := cloudAgentPrevisValidateEnum(operation.Motion, "动作", cloudAgentPrevisMotionIDs...); err != nil {
		return err
	}
	if len(motions) >= cloudAgentPrevisMaxActorMotions {
		return BadAuthRequest("单个演员最多32个动作片段")
	}
	start := 0.0
	if operation.Time != nil {
		start = *operation.Time
	}
	if err := cloudAgentPrevisFiniteInRange(start, "动作开始时间", 0, duration); err != nil {
		return err
	}
	length := math.Max(0.2, duration-start)
	if operation.Duration != nil {
		length = *operation.Duration
	}
	if err := cloudAgentPrevisFiniteInRange(length, "动作时长", 0.2, 60); err != nil {
		return err
	}
	if start+length > duration+1e-6 {
		return BadAuthRequest(fmt.Sprintf("动作 %.1f–%.1fs 超出共享表演时间线 %.1fs（最长镜头）；缩短 duration 或先用 shot_update 加长镜头", start, start+length, duration))
	}
	intensity := 1.0
	if operation.Intensity != nil {
		intensity = *operation.Intensity
	}
	if err := cloudAgentPrevisFiniteInRange(intensity, "动作幅度", 0.2, 2); err != nil {
		return err
	}
	id := fmt.Sprintf("%s-motion-%d", stringValue(object["id"]), len(motions)+1)
	if operation.KeyframeID != nil && *operation.KeyframeID != "" {
		if err := validateCloudAgentID(*operation.KeyframeID, "动作 id", 120); err != nil {
			return err
		}
		id = *operation.KeyframeID
	}
	for _, item := range motions {
		if stringValue(item["id"]) == id {
			return BadAuthRequest("动作 id 已存在")
		}
	}
	object["motions"] = append(motions, map[string]any{"id": id, "motion": operation.Motion, "start": start, "duration": length, "intensity": intensity})
	return nil
}

func cloudAgentPrevisRemoveActorMotion(object map[string]any, motions []map[string]any, keyframeID *string) error {
	if keyframeID == nil || *keyframeID == "" {
		return BadAuthRequest("删除动作需要用 keyframeId 传动作 id")
	}
	filtered := make([]map[string]any, 0, len(motions))
	for _, item := range motions {
		if stringValue(item["id"]) != *keyframeID {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == len(motions) {
		return BadAuthRequest("目标动作不存在")
	}
	object["motions"] = filtered
	return nil
}
