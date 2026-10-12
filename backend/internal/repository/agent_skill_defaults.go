package repository

import (
	"database/sql"
	"fmt"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"strconv"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentSkillDefaultRows 返回全部默认技能行，按 (position, skill_id) 稳定排序。
func (r *Repository) AgentSkillDefaultRows() ([]model.AgentSkillDefault, error) {
	var rows []model.AgentSkillDefault
	err := r.db.Where("scope = ?", "global").Order("position asc, skill_id asc").Find(&rows).Error
	return rows, err
}

// EnabledAgentSkillDefaults 返回仅启用的默认技能行，排序同 AgentSkillDefaultRows。
func (r *Repository) EnabledAgentSkillDefaults() ([]model.AgentSkillDefault, error) {
	var rows []model.AgentSkillDefault
	err := r.db.Where("scope = ? AND enabled = ?", "global", true).Order("position asc, skill_id asc").Find(&rows).Error
	return rows, err
}

// ReplaceAgentSkillDefaults 以 revision CAS 保护默认技能集合的整体替换：
// expectedRevision 不等于持久化修订号（初始为 0，清空后继续递增）时拒绝写入，
// 避免两名管理员基于同一旧版本互相覆盖。冲突时表内容保持不变。
func (r *Repository) ReplaceAgentSkillDefaults(expectedRevision int64, rows []model.AgentSkillDefault, updatedBy string) (int64, error) {
	var newRevision int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// The durable setting row survives an empty list. Take its write lock
		// before reading: both SQLite and PostgreSQL serialize independent
		// processes here, including the first save with no defaults yet.
		setting := model.SystemSetting{Key: agentSkillDefaultsRevisionKey, ValueJSON: "0"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&setting).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.SystemSetting{}).Where("key = ?", agentSkillDefaultsRevisionKey).Update("value_json", gorm.Expr("value_json")).Error; err != nil {
			return err
		}
		current, err := New(tx).agentSkillDefaultsRevision()
		if err != nil {
			return err
		}
		if current != expectedRevision {
			return kernel.AgentSkillDefaultsConflict(current)
		}
		if err := tx.Where("scope = ?", "global").Delete(&model.AgentSkillDefault{}).Error; err != nil {
			return err
		}
		newRevision = current + 1
		if err := tx.Model(&model.SystemSetting{}).Where("key = ?", agentSkillDefaultsRevisionKey).Updates(map[string]any{"value_json": strconv.FormatInt(newRevision, 10), "updated_by": updatedBy}).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		rows = append([]model.AgentSkillDefault(nil), rows...)
		for i := range rows {
			rows[i].Scope = "global"
			rows[i].Revision = current + 1
			rows[i].UpdatedBy = updatedBy
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return 0, err
	}
	return newRevision, nil
}

const agentSkillDefaultsRevisionKey = "agent_skill_defaults_revision"

func (r *Repository) agentSkillDefaultsRevision() (int64, error) {
	setting, err := r.SystemSettingOptional(agentSkillDefaultsRevisionKey)
	if err != nil {
		return 0, err
	}
	var revision int64
	if setting != nil {
		revision, err = strconv.ParseInt(setting.ValueJSON, 10, 64)
		if err != nil || revision < 0 {
			return 0, fmt.Errorf("invalid Agent skill defaults revision")
		}
	}
	// Bootstrap databases whose v51 rows predate the durable revision key.
	var rowRevision int64
	if err := r.db.Model(&model.AgentSkillDefault{}).Where("scope = ?", "global").Select("COALESCE(MAX(revision), 0)").Scan(&rowRevision).Error; err != nil {
		return 0, err
	}
	if rowRevision > revision {
		revision = rowRevision
	}
	return revision, nil
}

func (r *Repository) AgentSkillDefaultsSnapshot() (rows []model.AgentSkillDefault, revision int64, err error) {
	err = r.db.Transaction(func(tx *gorm.DB) error {
		repo := New(tx)
		var readErr error
		rows, readErr = repo.AgentSkillDefaultRows()
		if readErr != nil {
			return readErr
		}
		revision, readErr = repo.agentSkillDefaultsRevision()
		return readErr
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return
}

// AgentConversationSkills 返回一个会话冻结的技能集合，按 position 排序。
// 行按 conversation_id 隔离：不同会话互不可见。
func (r *Repository) AgentConversationSkills(conversationID string) ([]model.AgentConversationSkill, error) {
	var rows []model.AgentConversationSkill
	err := r.db.Where("conversation_id = ?", conversationID).Order("position asc, skill_id asc").Find(&rows).Error
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
