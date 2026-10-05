package app

import (
	"errors"
	"fmt"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/skills"

	"gorm.io/gorm"
)

// cloudAgentConversationIDFor 在创建运行前解析会话归属：新会话（parentID 为空）的
// 会话 ID 就是本轮运行 ID；续聊沿用父轮的会话 ID（父轮查不到时回退 parentID）。
// 语义必须与 newCloudAgentExecution 的继承逻辑一致——建 run 前技能集合的归属
// 依赖这里的判定，不能等插入执行记录时才解析。
func (s *Service) cloudAgentConversationIDFor(userID, runID, parentID string) string {
	if parentID == "" {
		return runID
	}
	if parent, err := s.repo.CloudAgent(userID, parentID); err == nil {
		return firstNonEmpty(parent.ConversationID, parentID)
	}
	return parentID
}

// agentSkillConversationRow 把解析出的技能条目转换为会话技能持久化行。
func agentSkillConversationRow(conversationID string, item skills.SkillSelectionItem, position int) model.AgentConversationSkill {
	return model.AgentConversationSkill{
		ConversationID: conversationID,
		SkillID:        item.SkillID,
		SkillVersionID: item.VersionID,
		ContentHash:    item.ContentHash,
		Source:         item.Source,
		Position:       position,
	}
}

// resolveRunSkills 解析本轮运行实际装配的技能集合并冻结来源。
//
// 新会话：全局默认（enabled 行）为最低层，本轮用户选择为最高层，按 skillId 去重后
// 由建 run 的事务全量落库（source=global/user），后续轮次以这份记录为基线，管理员再改默认不影响本会话。
// 既有会话：不重读全局默认，以既有行为基线，只追加/升级本轮新增的用户选择；
// 迁移前的旧会话没有基线行，行为与现状一致（只有本轮用户选择）。
// 两种路径最终都逐技能冻结快照并做容量准入，超限返回 400 agent_skill_budget_exceeded。
func (s *Service) resolveRunSkills(userID, conversationID string, userSkillIDs []string, isNewConversation bool) ([]cloudAgentSkill, error) {
	return s.resolveRunSkillsWithLayers(userID, conversationID, userSkillIDs, isNewConversation, nil, nil)
}

func skillSelectionItems(snapshots []cloudAgentSkill) []skills.SkillSelectionItem {
	items := make([]skills.SkillSelectionItem, 0, len(snapshots))
	for _, snapshot := range snapshots {
		items = append(items, skills.SkillSelectionItem{SkillID: snapshot.ID, VersionID: snapshot.VersionID, ContentHash: snapshot.Hash, Source: snapshot.Source})
	}
	return items
}

func (s *Service) resolveRunSkillsWithLayers(userID, conversationID string, userSkillIDs []string, isNewConversation bool, workspace, member []cloudAgentSkill) ([]cloudAgentSkill, error) {
	var resolved []skills.SkillSelectionItem
	if isNewConversation {
		defaults, err := s.repo.EnabledAgentSkillDefaults()
		if err != nil {
			return nil, err
		}
		globalLayer := make([]skills.SkillSelectionItem, 0, len(defaults))
		for _, row := range defaults {
			version, err := s.repo.SkillVersion(row.SkillVersionID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("默认技能 %s 引用的版本已失效，请联系管理员重新配置", row.SkillID), map[string]any{"skillId": row.SkillID})
				}
				return nil, err
			}
			if _, err := s.repo.Skill(row.SkillID); err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("默认技能 %s 不存在或已删除，请联系管理员重新配置", row.SkillID), map[string]any{"skillId": row.SkillID})
				}
				return nil, err
			}
			globalLayer = append(globalLayer, skills.SkillSelectionItem{
				SkillID: row.SkillID, VersionID: row.SkillVersionID,
				ContentHash: version.ContentHash, Source: skills.SkillSourceGlobal,
			})
		}
		userLayer := make([]skills.SkillSelectionItem, 0, len(userSkillIDs))
		for _, id := range userSkillIDs {
			// 用户选择沿用既有语义：必须是本人技能库中已安装且启用的技能；
			// 版本取当前安装版本，与 cloudAgentSkills 的冻结口径一致。
			detail, err := s.SkillDetail(userID, id)
			if err != nil {
				return nil, err
			}
			if !detail.IsAdded || detail.Status != 1 {
				return nil, BadAuthRequest("只能使用用户技能库中已安装且启用的技能")
			}
			userLayer = append(userLayer, skills.SkillSelectionItem{
				SkillID: id, VersionID: detail.VersionID,
				ContentHash: detail.ContentHash, Source: skills.SkillSourceUser,
			})
		}
		items, err := skills.ResolveSkillSelection(globalLayer, skillSelectionItems(workspace), skillSelectionItems(member), userLayer)
		if err != nil {
			return nil, err
		}
		resolved = items
	} else {
		baseline, err := s.repo.AgentConversationSkills(conversationID)
		if err != nil {
			return nil, err
		}
		resolved = make([]skills.SkillSelectionItem, 0, len(baseline)+len(userSkillIDs))
		existing := make(map[string]int, len(baseline))
		for _, row := range baseline {
			existing[row.SkillID] = len(resolved)
			resolved = append(resolved, skills.SkillSelectionItem{
				SkillID: row.SkillID, VersionID: row.SkillVersionID,
				ContentHash: row.ContentHash, Source: row.Source,
			})
		}
		selected := make(map[string]struct{}, len(userSkillIDs))
		for _, id := range userSkillIDs {
			if _, ok := selected[id]; ok {
				continue
			}
			selected[id] = struct{}{}
			// The browser resubmits all selected IDs on every turn. A previously
			// chosen user version stays pinned; choosing a global skill can upgrade
			// its source once, and selecting a new skill appends it.
			if at, ok := existing[id]; ok && resolved[at].Source == skills.SkillSourceUser {
				continue
			}
			// 用户显式选择的技能升级为 user 来源并跟踪其当前安装版本；
			// 只影响该技能行，其他默认技能的注入保持不变。
			detail, err := s.SkillDetail(userID, id)
			if err != nil {
				return nil, err
			}
			if !detail.IsAdded || detail.Status != 1 {
				return nil, BadAuthRequest("只能使用用户技能库中已安装且启用的技能")
			}
			item := skills.SkillSelectionItem{
				SkillID: id, VersionID: detail.VersionID,
				ContentHash: detail.ContentHash, Source: skills.SkillSourceUser,
			}
			if at, ok := existing[id]; ok {
				resolved[at] = item
			} else {
				existing[id] = len(resolved)
				resolved = append(resolved, item)
			}
		}
	}
	return s.freezeRunSkillSnapshots(userID, resolved)
}

// freezeRunSkillSnapshots 把解析结果逐技能冻结为运行快照：user 来源走既有
// SkillDetail + 冻结读路径（要求已安装），global 来源直读仓库版本并校验
// ContentHash（不要求用户安装）。全部就绪后统一做容量准入，超限即拒绝建 run。
func (s *Service) freezeRunSkillSnapshots(userID string, resolved []skills.SkillSelectionItem) ([]cloudAgentSkill, error) {
	snapshots := make([]cloudAgentSkill, 0, len(resolved))
	facts := make([]skills.SkillCapacityFacts, 0, len(resolved))
	for _, item := range resolved {
		if item.Source == skills.SkillSourceUser {
			snapshot, err := s.cloudAgentUserSkillSnapshotAtSelection(userID, item)
			if err != nil {
				return nil, err
			}
			snapshots = append(snapshots, snapshot.skill)
			facts = append(facts, skills.SkillCapacityFacts{SkillID: item.SkillID, FileCount: snapshot.fileCount, TotalBytes: snapshot.totalBytes, ContextBytes: snapshot.totalBytes})
			continue
		}
		version, err := s.repo.SkillVersion(item.VersionID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("默认技能 %s 的版本已失效，请联系管理员重新配置", item.SkillID), map[string]any{"skillId": item.SkillID})
			}
			return nil, err
		}
		if version.ContentHash != item.ContentHash {
			return nil, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("默认技能 %s 的内容已更新，请联系管理员重新配置", item.SkillID), map[string]any{"skillId": item.SkillID})
		}
		if _, err := s.skillDomain().GlobalSkillPackageFilesAtVersion(item.SkillID, item.VersionID, item.ContentHash); err != nil {
			return nil, err
		}
		files, err := s.repo.SkillFiles(version.ID)
		if err != nil {
			return nil, err
		}
		snapshot, err := cloudAgentSkillSnapshotFromFiles(item.SkillID, version, files)
		if err != nil {
			return nil, err
		}
		entry, err := s.skillDomain().GlobalSkillPackageFileAtVersion(item.SkillID, item.VersionID, item.ContentHash, cloudAgentSkillEntryPath)
		if err != nil {
			return nil, err
		}
		snapshot.Source = item.Source
		snapshot.Name, snapshot.Description = skills.SkillEntryMetadata(entry.Content)
		if snapshot.Name == "" {
			snapshot.Name = item.SkillID
		}
		snapshots = append(snapshots, *snapshot)
		facts = append(facts, skills.SkillCapacityFacts{SkillID: item.SkillID, FileCount: len(files), TotalBytes: version.TotalBytes, ContextBytes: version.TotalBytes})
	}
	if err := skills.AdmitSkillCapacity(facts, skills.SkillCapacityBudgets{
		MaxFiles:        skills.SkillRunMaxFiles,
		MaxTotalBytes:   skills.SkillRunMaxTotalBytes,
		MaxContextBytes: skills.SkillRunMaxContextBytes,
	}); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (s *Service) cloudAgentFrozenSkillFile(userID string, skill cloudAgentSkill, path string) (*SkillPackageFileContent, error) {
	if skill.Source == skills.SkillSourceGlobal || skill.Source == skills.SkillSourceWorkspace || skill.Source == skills.SkillSourceCrewMember {
		return s.skillDomain().GlobalSkillPackageFileAtVersion(skill.ID, firstNonEmpty(skill.VersionID, skill.Version), skill.Hash, path)
	}
	return s.SkillPackageFileAtVersion(userID, skill.ID, firstNonEmpty(skill.VersionID, skill.Version), skill.Hash, path)
}

func (s *Service) cloudAgentUserSkillSnapshotAtSelection(userID string, item skills.SkillSelectionItem) (cloudAgentSkillSnapshot, error) {
	// Keep the existing user-library display contract for the installed version;
	// older pinned versions take their metadata from the frozen entry instead.
	detail, err := s.SkillDetail(userID, item.SkillID)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	if detail.VersionID == item.VersionID && detail.ContentHash == item.ContentHash {
		current, err := s.cloudAgentUserSkillSnapshot(userID, item.SkillID)
		if err != nil {
			return cloudAgentSkillSnapshot{}, err
		}
		if current.skill.VersionID != item.VersionID || current.skill.Hash != item.ContentHash {
			return cloudAgentSkillSnapshot{}, creationConflict("技能在读取时已更新，请重试")
		}
		return current, nil
	}
	files, err := s.SkillPackageFilesAtVersion(userID, item.SkillID, item.VersionID, item.ContentHash)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	version, err := s.repo.SkillVersion(item.VersionID)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	modelFiles := make([]model.SkillFile, 0, len(files))
	for _, file := range files {
		modelFiles = append(modelFiles, model.SkillFile{Path: file.Path, SHA256: file.SHA256, Size: file.Size, MimeType: file.MimeType})
	}
	snapshot, err := cloudAgentSkillSnapshotFromFiles(item.SkillID, version, modelFiles)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	entry, err := s.SkillPackageFileAtVersion(userID, item.SkillID, item.VersionID, item.ContentHash, cloudAgentSkillEntryPath)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	snapshot.Source = skills.SkillSourceUser
	snapshot.Name, snapshot.Description = skills.SkillEntryMetadata(entry.Content)
	if snapshot.Name == "" {
		snapshot.Name = item.SkillID
	}
	return cloudAgentSkillSnapshot{skill: *snapshot, fileCount: len(files), totalBytes: version.TotalBytes}, nil
}

func cloudAgentConversationSkillRows(conversationID string, snapshots []cloudAgentSkill) []model.AgentConversationSkill {
	rows := make([]model.AgentConversationSkill, 0, len(snapshots))
	for position, snapshot := range snapshots {
		rows = append(rows, agentSkillConversationRow(conversationID, skills.SkillSelectionItem{SkillID: snapshot.ID, VersionID: snapshot.VersionID, ContentHash: snapshot.Hash, Source: snapshot.Source}, position))
	}
	return rows
}
