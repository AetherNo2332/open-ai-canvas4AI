package skills

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// Only known authorization, absence and corruption failures make a frozen run
// unrecoverable. Database/network/permission I/O errors remain retryable.
func IsPermanentFrozenSkillError(err error) bool {
	var integrity *skillPackageIntegrityError
	var app *kernel.AppError
	return errors.As(err, &integrity) || errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrChecksum) || errors.Is(err, io.ErrUnexpectedEOF) ||
		(errors.As(err, &app) && !app.Retryable && (app.Status == 400 || app.Status == 403 || app.Status == 404))
}

// SkillPackageFilesAtVersion reads an explicitly frozen version. It never
// consults Skill.CurrentVersionID, so an update cannot change an in-flight run.
func (s *Service) SkillPackageFilesAtVersion(userID, skillID, versionID, contentHash string) ([]SkillPackageFileItem, error) {
	skill, err := s.installedSkillForFrozenRead(userID, skillID)
	if err != nil {
		return nil, err
	}
	return s.skillPackageFilesAtFrozenVersion(skill, versionID, contentHash)
}

// GlobalSkillPackageFilesAtVersion is for trusted server-frozen default selections,
// never a replacement for user installation authorization on public HTTP reads.
func (s *Service) GlobalSkillPackageFilesAtVersion(skillID, versionID, contentHash string) ([]SkillPackageFileItem, error) {
	skill, err := s.publicSkillForFrozenRead(skillID)
	if err != nil {
		return nil, err
	}
	return s.skillPackageFilesAtFrozenVersion(skill, versionID, contentHash)
}

func (s *Service) publicSkillForFrozenRead(skillID string) (*model.Skill, error) {
	skill, err := s.repo.Skill(skillID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.AgentSkillDefaultsInvalid("默认技能 "+skillID+" 不存在或已停用", map[string]any{"skillId": skillID})
	}
	if err != nil {
		return nil, err
	}
	if skill.IsPrivate {
		return nil, kernel.AgentSkillDefaultsInvalid("默认技能 "+skillID+" 已变为私有技能", map[string]any{"skillId": skillID})
	}
	return skill, nil
}

func (s *Service) skillPackageFilesAtFrozenVersion(skill *model.Skill, versionID, contentHash string) ([]SkillPackageFileItem, error) {
	version, err := s.repo.SkillVersion(versionID)
	if err != nil {
		return nil, err
	}
	if version.SkillID != skill.ID || strings.TrimSpace(contentHash) == "" || version.ContentHash != contentHash {
		return nil, kernel.BadAuthRequest("技能冻结版本不匹配")
	}
	files, err := s.repo.SkillFiles(version.ID)
	if err != nil {
		return nil, err
	}
	frozen := &model.Skill{ID: skill.ID, ContentHash: version.ContentHash, CurrentVersionID: version.ID, FileCount: version.FileCount, TotalBytes: version.TotalBytes}
	if err := validateSkillPackageSnapshot(s, frozen, version, files); err != nil {
		return nil, err
	}
	items := make([]SkillPackageFileItem, 0, len(files))
	for _, file := range files {
		items = append(items, skillFileItem(file))
	}
	return items, nil
}

// SkillPackageFileAtVersion is the content counterpart of
// SkillPackageFilesAtVersion and performs the same frozen-version checks.
func (s *Service) SkillPackageFileAtVersion(userID, skillID, versionID, contentHash, filePath string) (*SkillPackageFileContent, error) {
	skill, err := s.installedSkillForFrozenRead(userID, skillID)
	if err != nil {
		return nil, err
	}
	return s.skillPackageFileAtFrozenVersion(skill, versionID, contentHash, filePath)
}

func (s *Service) GlobalSkillPackageFileAtVersion(skillID, versionID, contentHash, filePath string) (*SkillPackageFileContent, error) {
	skill, err := s.publicSkillForFrozenRead(skillID)
	if err != nil {
		return nil, err
	}
	return s.skillPackageFileAtFrozenVersion(skill, versionID, contentHash, filePath)
}

func (s *Service) skillPackageFileAtFrozenVersion(skill *model.Skill, versionID, contentHash, filePath string) (*SkillPackageFileContent, error) {
	version, err := s.repo.SkillVersion(versionID)
	if err != nil {
		return nil, err
	}
	if version.SkillID != skill.ID || strings.TrimSpace(contentHash) == "" || version.ContentHash != contentHash {
		return nil, kernel.BadAuthRequest("技能冻结版本不匹配")
	}
	files, err := s.repo.SkillFiles(version.ID)
	if err != nil {
		return nil, err
	}
	frozen := &model.Skill{ID: skill.ID, ContentHash: version.ContentHash, CurrentVersionID: version.ID, FileCount: version.FileCount, TotalBytes: version.TotalBytes}
	if err := validateSkillPackageSnapshot(s, frozen, version, files); err != nil {
		return nil, err
	}
	path, err := normalizeSkillPath(filePath)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.Path != path {
			continue
		}
		content, err := s.readSkillArchiveEntry(version, path)
		if err != nil {
			return nil, err
		}
		if !isPreviewText(file.MimeType, file.Path) || !isFrozenSkillText(content) {
			return &SkillPackageFileContent{File: skillFileItem(file), Binary: true}, nil
		}
		if len(content) > maxSkillPreviewBytes {
			return nil, kernel.BadAuthRequest("文件超过 512KB，请下载后查看")
		}
		return &SkillPackageFileContent{File: skillFileItem(file), Content: string(content)}, nil
	}
	return nil, kernel.BadAuthRequest("技能文件不存在")
}

func isFrozenSkillText(content []byte) bool {
	if !utf8.Valid(content) {
		return false
	}
	for _, char := range string(content) {
		if unicode.IsControl(char) && char != '\n' && char != '\r' && char != '\t' {
			return false
		}
	}
	return true
}

// Frozen content does not freeze authorization: uninstall/disable applies to
// subsequent reads and worker restarts without changing the selected version.
func (s *Service) installedSkillForFrozenRead(userID, skillID string) (*model.Skill, error) {
	skill, err := s.visibleSkill(userID, skillID)
	if err != nil {
		return nil, err
	}
	if skill.Status != 1 {
		return nil, kernel.Forbidden("技能已停用")
	}
	if skill.OwnerID != userID {
		state, err := s.skillState(userID, skillID)
		if err != nil {
			return nil, err
		}
		if !state.Added {
			return nil, kernel.Forbidden("技能已从用户技能库移除")
		}
	}
	return skill, nil
}

func SkillEntryMetadata(content string) (name, description string) {
	metadata := parseSkillPackageMetadata([]byte(content))
	return metadata.Name, metadata.Description
}

// skillPackageIntegrityError 标记冻结版本的包内容与登记的哈希不一致，Agent 不得读取。
type skillPackageIntegrityError struct{ error }

// invalidSkillPackage 标记数据库元数据与 ZIP 内容不一致这类不可恢复的损坏，冻结运行据此终止而不是重试。
func invalidSkillPackage(format string, args ...any) error {
	return &skillPackageIntegrityError{fmt.Errorf(format, args...)}
}
