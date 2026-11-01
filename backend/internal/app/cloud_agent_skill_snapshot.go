package app

import (
	"fmt"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/skills"
)

type cloudAgentSkill struct {
	Source       string            `json:"source,omitempty"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Version      string            `json:"version"`
	Hash         string            `json:"hash"`
	Instruction  string            `json:"instruction,omitempty"`
	Files        map[string]string `json:"files,omitempty"`
	NativeName   string            `json:"nativeName,omitempty"`
	VersionID    string            `json:"versionId,omitempty"`
	VersionLabel string            `json:"versionLabel,omitempty"`
	NativeFiles  []PiSkillFile     `json:"nativeFiles,omitempty"`
}

const cloudAgentSkillEntryPath = "SKILL.md"

func (s *Service) cloudAgentSkills(userID string, ids []string) ([]cloudAgentSkill, error) {
	snapshots := []cloudAgentSkill{}
	for _, id := range ids {
		snapshot, err := s.cloudAgentUserSkillSnapshot(userID, id)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot.skill)
	}
	return snapshots, nil
}

// cloudAgentUserSkillSnapshot 冻结单个"用户技能库来源"的技能快照：要求已安装且启用，
// 版本与内容 hash 在这里定格，正文按需读取。
func (s *Service) cloudAgentUserSkillSnapshot(userID, id string) (cloudAgentSkillSnapshot, error) {
	skill, err := s.SkillDetail(userID, id)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	if !skill.IsAdded || skill.Status != 1 {
		return cloudAgentSkillSnapshot{}, BadAuthRequest("只能使用用户技能库中已安装且启用的技能")
	}
	files, err := s.SkillPackageFilesAtVersion(userID, id, skill.VersionID, skill.ContentHash)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	// Detect an update during package reads instead of mixing two versions.
	latest, err := s.SkillDetail(userID, id)
	if err != nil {
		return cloudAgentSkillSnapshot{}, err
	}
	if latest.VersionID != skill.VersionID || latest.ContentHash != skill.ContentHash {
		return cloudAgentSkillSnapshot{}, creationConflict("技能在读取时已更新，请重试")
	}
	snapshot := cloudAgentSkill{ID: id, Name: skill.SkillName, Description: skill.Description,
		Source:  skills.SkillSourceUser,
		Version: skill.VersionID, VersionID: skill.VersionID, VersionLabel: skill.Version,
		NativeName: nativeSkillName(skill.SkillName, id), Hash: skill.ContentHash,
		Files: map[string]string{cloudAgentSkillEntryPath: ""}}
	for _, file := range files {
		// The entry is listed separately; file bodies are fetched on demand.
		if file.Path == cloudAgentSkillEntryPath {
			continue
		}
		// Executable/binary packages are never executed; text references are data only.
		if !isNativeSkillTextPath(file.Path) {
			continue
		}
		snapshot.Files[file.Path] = ""
	}
	for _, file := range files {
		if file.Path == cloudAgentSkillEntryPath || isNativeSkillTextPath(file.Path) {
			snapshot.NativeFiles = append(snapshot.NativeFiles, PiSkillFile{Path: file.Path, SHA256: file.SHA256,
				Size: file.Size, MimeType: file.MimeType, Text: true})
		}
	}
	return cloudAgentSkillSnapshot{skill: snapshot, fileCount: len(files), totalBytes: skill.TotalBytes}, nil
}

// cloudAgentSkillSnapshot 携带技能快照与容量准入所需的体积事实。
type cloudAgentSkillSnapshot struct {
	skill      cloudAgentSkill
	fileCount  int
	totalBytes int64
}

// cloudAgentSkillSnapshotFromFiles 从仓库版本行直接冻结"全局默认来源"的技能快照：
// 不要求用户安装该技能；文件清单来自 SkillFiles，正文同样按需读取。
func cloudAgentSkillSnapshotFromFiles(skillID string, version *model.SkillVersion, files []model.SkillFile) (*cloudAgentSkill, error) {
	name := skillID
	description := ""
	entry := ""
	// SKILL.md 的 frontmatter 提供展示名与描述；缺失时回退技能 ID，
	// 不让快照装配因元数据不全而失败。
	for _, file := range files {
		if file.Path == cloudAgentSkillEntryPath {
			entry = file.Path
		}
	}
	if entry == "" {
		return nil, kernel.AgentSkillDefaultsInvalid(fmt.Sprintf("默认技能 %s 的版本缺少 SKILL.md 入口", skillID), map[string]any{"skillId": skillID})
	}
	snapshot := &cloudAgentSkill{ID: skillID, Name: name, Description: description,
		Version: version.VersionLabel, VersionID: version.ID, VersionLabel: version.VersionLabel,
		NativeName: nativeSkillName(name, skillID), Hash: version.ContentHash,
		Files: map[string]string{cloudAgentSkillEntryPath: ""}}
	for _, file := range files {
		if file.Path == cloudAgentSkillEntryPath || !isNativeSkillTextPath(file.Path) {
			continue
		}
		snapshot.Files[file.Path] = ""
	}
	for _, file := range files {
		if file.Path == cloudAgentSkillEntryPath || isNativeSkillTextPath(file.Path) {
			snapshot.NativeFiles = append(snapshot.NativeFiles, PiSkillFile{Path: file.Path, SHA256: file.SHA256,
				Size: file.Size, MimeType: file.MimeType, Text: true})
		}
	}
	return snapshot, nil
}
