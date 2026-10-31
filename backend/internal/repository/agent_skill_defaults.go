package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"

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
		repo := r.transactionRepository(tx)
		current, err := repo.LockAgentSkillDefaults()
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
		if err := repo.SetAgentSkillDefaultsRevision(newRevision, updatedBy); err != nil {
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

const (
	agentSkillDefaultsRevisionKey = "agent_skill_defaults_revision"
	// agentSkillDefaultsSeededKey 记录平台启动时补种过的默认技能 ID（JSON 字符串数组）。
	agentSkillDefaultsSeededKey = "agent_skill_defaults_platform_seeded"
)

// LockAgentSkillDefaults 取得默认技能修订行的写锁并返回当前修订号，必须在 Transaction 内调用。
// 修订行在清空列表后仍保留：SQLite 与 PostgreSQL 都在这里串行化独立进程，
// 包括尚无任何默认技能时的第一次写入。
func (r *Repository) LockAgentSkillDefaults() (int64, error) {
	setting := model.SystemSetting{Key: agentSkillDefaultsRevisionKey, ValueJSON: "0"}
	if err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&setting).Error; err != nil {
		return 0, err
	}
	// 自赋值只为取行锁；UpdateColumn 不改 updated_at，没有变化的启动不留下写入痕迹。
	if err := r.db.Model(&model.SystemSetting{}).Where("key = ?", agentSkillDefaultsRevisionKey).UpdateColumn("value_json", gorm.Expr("value_json")).Error; err != nil {
		return 0, err
	}
	return r.agentSkillDefaultsRevision()
}

// SetAgentSkillDefaultsRevision 写入新修订号；调用方须在同一事务内持有 LockAgentSkillDefaults 的锁。
func (r *Repository) SetAgentSkillDefaultsRevision(revision int64, updatedBy string) error {
	return r.db.Model(&model.SystemSetting{}).Where("key = ?", agentSkillDefaultsRevisionKey).Updates(map[string]any{"value_json": strconv.FormatInt(revision, 10), "updated_by": updatedBy}).Error
}

// CreateAgentSkillDefaultIfMissing 插入一行全局默认技能；(scope, skill_id) 已存在时不写入并返回 false。
func (r *Repository) CreateAgentSkillDefaultIfMissing(row *model.AgentSkillDefault) (bool, error) {
	row.Scope = "global"
	result := r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}, {Name: "skill_id"}}, DoNothing: true}).Create(row)
	return result.RowsAffected == 1, result.Error
}

// UpdateAgentSkillDefaultVersion 把一行全局默认技能从 fromVersionID 改指 toVersionID，
// 不改 enabled 与 position；行已不在旧版本时返回 false。
func (r *Repository) UpdateAgentSkillDefaultVersion(skillID, fromVersionID, toVersionID string, revision int64, updatedBy string) (bool, error) {
	result := r.db.Model(&model.AgentSkillDefault{}).
		Where("scope = ? AND skill_id = ? AND skill_version_id = ?", "global", skillID, fromVersionID).
		Updates(map[string]any{"skill_version_id": toVersionID, "revision": revision, "updated_by": updatedBy})
	return result.RowsAffected == 1, result.Error
}

// AgentSkillDefaultsSeeded 返回平台已补种过的默认技能 ID；从未补种时返回空。
func (r *Repository) AgentSkillDefaultsSeeded() ([]string, error) {
	setting, err := r.SystemSettingOptional(agentSkillDefaultsSeededKey)
	if err != nil || setting == nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(setting.ValueJSON), &ids); err != nil {
		return nil, fmt.Errorf("invalid Agent skill defaults seed marker: %w", err)
	}
	return ids, nil
}

// SaveAgentSkillDefaultsSeeded 覆盖平台补种标记，保留设置行的 created_at。
func (r *Repository) SaveAgentSkillDefaultsSeeded(ids []string, updatedBy string) error {
	body, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	setting := model.SystemSetting{Key: agentSkillDefaultsSeededKey, ValueJSON: string(body), UpdatedBy: updatedBy}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value_json", "updated_by", "updated_at"}),
	}).Create(&setting).Error
}

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
