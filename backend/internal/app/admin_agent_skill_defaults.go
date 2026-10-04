package app

import (
	"errors"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/skills"

	"gorm.io/gorm"
)

// AgentSkillDefaultItem 是管理员保存默认技能集合时的单行输入。
// Enabled 用 0/1 整数便于 JSON 往返；GET 与 PUT 使用同一结构。
type AgentSkillDefaultItem struct {
	SkillID        string `json:"skillId"`
	SkillVersionID string `json:"skillVersionId"`
	Position       int    `json:"position"`
	Enabled        int    `json:"enabled"`
}

// AgentSkillDefaultsViewItem 是默认技能列表的单行展示，附带技能与版本的实时快照。
type AgentSkillDefaultsViewItem struct {
	SkillID        string `json:"skillId"`
	SkillName      string `json:"skillName"`
	SkillVersionID string `json:"skillVersionId"`
	VersionLabel   string `json:"versionLabel"`
	Position       int    `json:"position"`
	Enabled        int    `json:"enabled"`
	Status         int    `json:"status"`
	FileCount      int    `json:"fileCount"`
	TotalBytes     int64  `json:"totalBytes"`
}

// AgentSkillDefaultsView 是管理员默认技能配置的整体视图。
// totals 只统计 enabled 行；ContextEstimateBytes = Σ enabled 技能的 SkillVersion.TotalBytes，
// 是"运行时实际读入上下文的字节数"的保守上限代理口径（假设整个技能包都被读入），
// 真实用量通常低于该值。
type AgentSkillDefaultsView struct {
	Revision             int64                        `json:"revision"`
	Items                []AgentSkillDefaultsViewItem `json:"items"`
	TotalFiles           int                          `json:"totalFiles"`
	TotalBytes           int64                        `json:"totalBytes"`
	ContextEstimateBytes int64                        `json:"contextEstimateBytes"`
}

// AdminAgentSkillDefaults 返回默认技能配置视图：行按 position 排序，
// 每行附带技能/版本的当前状态，便于管理端发现引用了已禁用或已删除技能的行。
func (s *Service) AdminAgentSkillDefaults(actor *model.User) (*AgentSkillDefaultsView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	rows, revision, err := s.repo.AgentSkillDefaultsSnapshot()
	if err != nil {
		return nil, err
	}
	view := &AgentSkillDefaultsView{Items: make([]AgentSkillDefaultsViewItem, 0, len(rows))}
	for _, row := range rows {
		item := AgentSkillDefaultsViewItem{
			SkillID:        row.SkillID,
			SkillVersionID: row.SkillVersionID,
			Position:       row.Position,
			Enabled:        boolToInt(row.Enabled),
		}
		skill, skillErr := s.repo.Skill(row.SkillID)
		if skillErr != nil {
			if !errors.Is(skillErr, gorm.ErrRecordNotFound) {
				return nil, skillErr
			}
			// 引用的技能已被删除：保留行以暴露配置悬空，但快照字段留空且不计入总量。
			view.Items = append(view.Items, item)
			continue
		}
		item.SkillName = skill.Name
		item.Status = skill.Status
		version, versionErr := s.repo.SkillVersion(row.SkillVersionID)
		if versionErr != nil {
			if !errors.Is(versionErr, gorm.ErrRecordNotFound) {
				return nil, versionErr
			}
			view.Items = append(view.Items, item)
			continue
		}
		item.VersionLabel = version.VersionLabel
		item.FileCount = version.FileCount
		item.TotalBytes = version.TotalBytes
		if row.Enabled {
			view.TotalFiles += version.FileCount
			view.TotalBytes += version.TotalBytes
			// 保守上限：假设启用的技能包整体进入上下文，真实读取量通常低于该估计。
			view.ContextEstimateBytes += version.TotalBytes
		}
		view.Items = append(view.Items, item)
	}
	view.Revision = revision
	return view, nil
}

// ReplaceAgentSkillDefaults 以 CAS 整体替换默认技能集合。
// 校验顺序：RequireAdmin → 逐项校验（存在、启用、非私有、版本归属）→ enabled 集合容量预检 → 互斥保存。
func (s *Service) ReplaceAgentSkillDefaults(actor *model.User, revision int64, items []AgentSkillDefaultItem) (int64, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return 0, err
	}
	rows := make([]model.AgentSkillDefault, 0, len(items))
	enabledFacts := make([]skills.SkillCapacityFacts, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item.SkillID] {
			return 0, kernel.AgentSkillDefaultsInvalid("默认技能重复："+item.SkillID, map[string]any{"skillId": item.SkillID})
		}
		seen[item.SkillID] = true
		row, facts, err := s.validateAgentSkillDefaultItem(item)
		if err != nil {
			return 0, err
		}
		rows = append(rows, row)
		if row.Enabled {
			enabledFacts = append(enabledFacts, facts)
		}
	}
	// 容量预算只约束 enabled 集合；disabled 行不参与运行时装配，无需计入。
	if err := skills.AdmitSkillCapacity(enabledFacts, skills.SkillCapacityBudgets{
		MaxFiles:        skills.SkillRunMaxFiles,
		MaxTotalBytes:   skills.SkillRunMaxTotalBytes,
		MaxContextBytes: skills.SkillRunMaxContextBytes,
	}); err != nil {
		return 0, err
	}
	// Repository serializes the durable revision row across processes. This
	// local lock additionally avoids competing SQLite writers in one service.
	s.agentSkillDefaultsMu.Lock()
	defer s.agentSkillDefaultsMu.Unlock()
	return s.repo.ReplaceAgentSkillDefaults(revision, rows, actor.ID)
}

// validateAgentSkillDefaultItem 校验单个默认技能行：技能存在且启用、非私有、
// 版本确实属于该技能。违规统一返回 kernel.AgentSkillDefaultsInvalid，message 携带技能 ID。
func (s *Service) validateAgentSkillDefaultItem(item AgentSkillDefaultItem) (model.AgentSkillDefault, skills.SkillCapacityFacts, error) {
	skillID := item.SkillID
	skill, err := s.repo.Skill(skillID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.AgentSkillDefault{}, skills.SkillCapacityFacts{}, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("技能 %s 不存在或已删除", skillID), map[string]any{"skillId": skillID})
		}
		return model.AgentSkillDefault{}, skills.SkillCapacityFacts{}, err
	}
	// repo.Skill 只按 status=1 查询，这里显式区分"不存在"与"已禁用"，避免禁用技能被误报为不存在。
	if skill.Status != 1 {
		return model.AgentSkillDefault{}, skills.SkillCapacityFacts{}, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("技能 %s 已被禁用，不能设为默认技能", skillID), map[string]any{"skillId": skillID})
	}
	if skill.IsPrivate {
		return model.AgentSkillDefault{}, skills.SkillCapacityFacts{}, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("技能 %s 是私有技能，不能设为默认技能", skillID), map[string]any{"skillId": skillID})
	}
	version, err := s.repo.SkillVersion(item.SkillVersionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.AgentSkillDefault{}, skills.SkillCapacityFacts{}, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("技能 %s 引用的版本 %s 不存在", skillID, item.SkillVersionID), map[string]any{"skillId": skillID})
		}
		return model.AgentSkillDefault{}, skills.SkillCapacityFacts{}, err
	}
	if version.SkillID != skillID {
		return model.AgentSkillDefault{}, skills.SkillCapacityFacts{}, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("版本 %s 不属于技能 %s", item.SkillVersionID, skillID), map[string]any{"skillId": skillID})
	}
	return model.AgentSkillDefault{
		ID:             newID(),
		SkillID:        skillID,
		SkillVersionID: item.SkillVersionID,
		Position:       item.Position,
		Enabled:        item.Enabled != 0,
	}, skills.SkillCapacityFacts{SkillID: skillID, FileCount: version.FileCount, TotalBytes: version.TotalBytes, ContextBytes: version.TotalBytes}, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type AgentSkillDefaultsSummary struct {
	Count  int                             `json:"count"`
	Skills []AgentSkillDefaultsSummaryItem `json:"skills"`
}

type AgentSkillDefaultsSummaryItem struct {
	SkillID   string `json:"skillId"`
	SkillName string `json:"skillName"`
}

func (s *Service) AgentSkillDefaultsForUser(userID string) (*AgentSkillDefaultsSummary, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, kernel.BadAuthRequest("用户 ID 不能为空")
	}
	rows, err := s.repo.EnabledAgentSkillDefaults()
	if err != nil {
		return nil, err
	}
	view := &AgentSkillDefaultsSummary{Skills: make([]AgentSkillDefaultsSummaryItem, 0, len(rows))}
	for _, row := range rows {
		skill, err := s.repo.Skill(row.SkillID)
		if err != nil {
			return nil, err
		}
		view.Skills = append(view.Skills, AgentSkillDefaultsSummaryItem{SkillID: row.SkillID, SkillName: skill.Name})
	}
	view.Count = len(view.Skills)
	return view, nil
}
