package previs

import "math"

// ApplySceneUpdate preserves the original field order and in-memory mutation
// semantics. The adapters own their document copies and atomic persistence.
func ApplySceneUpdate(scene map[string]any, operation SceneUpdate, policy IDPolicy) (EditResult, error) {
	id, _ := scene["id"].(string)
	if operation.ID != id {
		return EditResult{}, invalid("scene_update 的 id 必须是当前场景 ID")
	}
	if operation.Title != nil {
		if err := validateText(*operation.Title, "场景标题", 160, true); err != nil {
			return EditResult{}, err
		}
		scene["title"] = *operation.Title
	}
	if operation.Background != nil {
		if err := validateColor(*operation.Background); err != nil {
			return EditResult{}, err
		}
		scene["background"] = *operation.Background
	}
	if operation.EnvironmentIntensity != nil {
		value := *operation.EnvironmentIntensity
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return EditResult{}, invalid("environmentIntensity超出限制")
		}
		scene["environmentIntensity"] = value
	}
	if operation.GridVisible != nil {
		scene["gridVisible"] = *operation.GridVisible
	}
	if operation.ActiveShotID != nil {
		if err := validateActiveID(*operation.ActiveShotID, policy); err != nil {
			return EditResult{}, err
		}
		scene["activeShotId"] = *operation.ActiveShotID
	}
	title, _ := scene["title"].(string)
	return EditResult{Operation: "scene_update", ID: id, Title: title, Summary: "修改场景级设置"}, nil
}
