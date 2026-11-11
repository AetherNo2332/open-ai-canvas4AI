package database

import "yingce/backend/internal/model"

// Models 是当前应用持久化表的唯一清单，跨数据库迁移必须完整覆盖。
func Models() []any {
	return append(baselineModels(), &model.SkillCurationSetting{}, &model.SkillCurationCategory{},
		&model.SkillCurationAssignment{}, &model.SkillCurationRoot{}, &model.SkillCurationRootAssignment{})
}

// baselineModels preserves v1 initialization; curation tables are created by
// v46/v47 so those migrations keep their historical upgrade/rollback boundary.
func baselineModels() []any {
	return append(append(appModels(), AgentRuntimeModels()...), &model.PlatformSubmission{}, &model.PlatformTaskEvent{}, &model.PlatformEventCounter{})
}
