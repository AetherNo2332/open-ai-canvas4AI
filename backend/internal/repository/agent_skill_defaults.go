package repository

import (
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentSkillDefaultRows 返回全部默认技能行，按 (position, skill_id) 稳定排序。
func (r *Repository) AgentSkillDefaultRows() ([]model.AgentSkillDefault, error) {
	var rows []model.AgentSkillDefault
	err := r.db.Order("position asc, skill_id asc").Find(&rows).Error
	return rows, err
}

// EnabledAgentSkillDefaults 返回仅启用的默认技能行，排序同 AgentSkillDefaultRows。
func (r *Repository) EnabledAgentSkillDefaults() ([]model.AgentSkillDefault, error) {
	var rows []model.AgentSkillDefault
	err := r.db.Where("enabled = ?", true).Order("position asc, skill_id asc").Find(&rows).Error
	return rows, err
}

// ReplaceAgentSkillDefaults 以 revision CAS 保护默认技能集合的整体替换：
// expectedRevision 不等于现有 max(revision)（空表为 0）时拒绝写入，
// 避免两名管理员基于同一旧版本互相覆盖。冲突时表内容保持不变。
func (r *Repository) ReplaceAgentSkillDefaults(expectedRevision int64, rows []model.AgentSkillDefault, updatedBy string) (int64, error) {
	var newRevision int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current int64
		if err := tx.Model(&model.AgentSkillDefault{}).Select("COALESCE(MAX(revision), 0)").Scan(&current).Error; err != nil {
			return err
		}
		if current != expectedRevision {
			return kernel.AgentSkillDefaultsConflict(current)
		}
		if err := tx.Where("scope = ?", "global").Delete(&model.AgentSkillDefault{}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			newRevision = current + 1
			return nil
		}
		for i := range rows {
			rows[i].Scope = "global"
			rows[i].Revision = current + 1
			rows[i].UpdatedBy = updatedBy
		}
		newRevision = current + 1
		return tx.Create(&rows).Error
	})
	if err != nil {
		return 0, err
	}
	return newRevision, nil
}

// AgentConversationSkills 返回一个会话冻结的技能集合，按 position 排序。
// 行按 conversation_id 隔离：不同会话互不可见。
func (r *Repository) AgentConversationSkills(conversationID string) ([]model.AgentConversationSkill, error) {
	var rows []model.AgentConversationSkill
	err := r.db.Where("conversation_id = ?", conversationID).Order("position asc").Find(&rows).Error
	return rows, err
}

// SaveAgentConversationSkills 按 (conversation_id, skill_id) upsert，
// 覆盖装配时冻结的版本、内容指纹、来源与顺序，不产生重复行。
func (r *Repository) SaveAgentConversationSkills(rows []model.AgentConversationSkill) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_id"}, {Name: "skill_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"skill_version_id", "content_hash", "source", "position"}),
	}).Create(&rows).Error
}
