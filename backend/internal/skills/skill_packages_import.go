package skills

import (
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/repository"
)

const maxSkillCollectionSkills = 64

type SkillUploadPreviewItem struct {
	EntryPath   string `json:"entryPath"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	FileCount   int    `json:"fileCount"`
	TotalBytes  int64  `json:"totalBytes"`
}

type SkillUploadPreview struct {
	Skills           []SkillUploadPreviewItem `json:"skills"`
	IgnoredFileCount int                      `json:"ignoredFileCount"`
	TotalBytes       int64                    `json:"totalBytes"`
}

type SkillUploadInstallResult struct {
	Skills           []SkillItem `json:"skills"`
	Skill            *SkillItem  `json:"skill,omitempty"`
	IgnoredFileCount int         `json:"ignoredFileCount"`
}

type skillUploadArchive struct {
	entryPath string
	archive   skillPackageArchive
}

func readSkillUpload(sourceType string, header *multipart.FileHeader) ([]byte, string, error) {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	if sourceType == "" && header != nil {
		switch strings.ToLower(path.Ext(header.Filename)) {
		case ".md", ".markdown":
			sourceType = "markdown"
		case ".zip":
			sourceType = "zip"
		}
	}
	if sourceType != "markdown" && sourceType != "zip" {
		return nil, sourceType, kernel.BadAuthRequest("技能文件仅支持 Markdown 或 ZIP")
	}
	if header == nil || header.Size <= 0 || header.Size > maxSkillPackageBytes {
		return nil, sourceType, kernel.BadAuthRequest("技能文件大小必须在 1B-20MB 之间")
	}
	file, err := header.Open()
	if err != nil {
		return nil, sourceType, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSkillPackageBytes+1))
	if err != nil {
		return nil, sourceType, err
	}
	if len(data) > maxSkillPackageBytes {
		return nil, sourceType, kernel.BadAuthRequest("技能文件不能超过 20MB")
	}
	return data, sourceType, nil
}

func parseSkillUploadArchives(data []byte, sourceType string, req SkillInstallRequest) ([]skillUploadArchive, int, error) {
	if sourceType == "markdown" {
		archive, err := archiveFromMarkdown(data, req.Name, req.Description)
		if err != nil {
			return nil, 0, err
		}
		return []skillUploadArchive{{entryPath: "SKILL.md", archive: archive}}, 0, nil
	}
	raw, err := readSkillZipFiles(data)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) > maxSkillPackageFiles {
		return nil, 0, kernel.BadAuthRequest("技能合集中所有文件合计不能超过 512 个")
	}
	roots := map[string]map[string][]byte{}
	for filePath := range raw {
		if path.Base(filePath) == "SKILL.md" {
			roots[path.Dir(filePath)] = map[string][]byte{}
		}
	}
	if len(roots) == 0 {
		return nil, 0, kernel.BadAuthRequest("技能包中缺少 SKILL.md；合集请包含各技能目录，不要只放嵌套 ZIP")
	}
	if len(roots) > maxSkillCollectionSkills {
		return nil, 0, kernel.BadAuthRequest("一个技能合集最多安装 64 个技能")
	}
	if len(roots) > 1 && (strings.TrimSpace(req.Name) != "" || strings.TrimSpace(req.Description) != "") {
		return nil, 0, kernel.BadAuthRequest("合集使用各技能自己的名称和简介，请清空覆盖名称与覆盖简介")
	}
	ignored := 0
	for filePath, content := range raw {
		// Nested Skills own their files; a parent never absorbs another entry.
		for dir := path.Dir(filePath); ; dir = path.Dir(dir) {
			if files, ok := roots[dir]; ok {
				relative := filePath
				if dir != "." {
					relative = strings.TrimPrefix(filePath, dir+"/")
				}
				files[relative] = content
				break
			}
			if dir == "." {
				ignored++
				break
			}
		}
	}
	paths := make([]string, 0, len(roots))
	for root := range roots {
		paths = append(paths, root)
	}
	sort.Strings(paths)
	names := map[string]bool{}
	archives := make([]skillUploadArchive, 0, len(paths))
	for _, root := range paths {
		files := roots[root]
		if len(files) > maxSkillPackageFiles {
			return nil, 0, kernel.BadAuthRequest("单个技能文件数量不能超过 512 个")
		}
		archive, err := finalizeSkillArchive(files, parseSkillPackageMetadata(files["SKILL.md"]))
		if err != nil {
			return nil, 0, kernel.BadAuthRequest(fmt.Sprintf("技能目录 %s：%s", root, err.Error()))
		}
		name := strings.ToLower(strings.TrimSpace(archive.Metadata.Name))
		if names[name] {
			return nil, 0, kernel.BadAuthRequest("技能合集中存在重复名称：" + archive.Metadata.Name)
		}
		names[name] = true
		archives = append(archives, skillUploadArchive{entryPath: path.Join(root, "SKILL.md"), archive: archive})
	}
	return archives, ignored, nil
}

func (s *Service) PreviewSkillUpload(sourceType string, header *multipart.FileHeader) (*SkillUploadPreview, error) {
	data, sourceType, err := readSkillUpload(sourceType, header)
	if err != nil {
		return nil, err
	}
	archives, ignored, err := parseSkillUploadArchives(data, sourceType, SkillInstallRequest{})
	if err != nil {
		return nil, err
	}
	result := &SkillUploadPreview{Skills: make([]SkillUploadPreviewItem, 0, len(archives)), IgnoredFileCount: ignored}
	for _, item := range archives {
		metadata := item.archive.Metadata
		result.Skills = append(result.Skills, SkillUploadPreviewItem{EntryPath: item.entryPath, Name: metadata.Name, Description: metadata.Description, Version: metadata.Version, FileCount: len(item.archive.Files), TotalBytes: item.archive.TotalBytes})
		result.TotalBytes += item.archive.TotalBytes
	}
	return result, nil
}

func (s *Service) InstallSkillUploadCollection(userID, sourceType string, header *multipart.FileHeader, req SkillInstallRequest) (*SkillUploadInstallResult, error) {
	data, sourceType, err := readSkillUpload(sourceType, header)
	if err != nil {
		return nil, err
	}
	archives, ignored, err := parseSkillUploadArchives(data, sourceType, req)
	if err != nil {
		return nil, err
	}
	result := &SkillUploadInstallResult{Skills: make([]SkillItem, 0, len(archives)), IgnoredFileCount: ignored}
	err = s.repo.Transaction(func(repo *repository.Repository) error {
		domain := New(repo, s.dataDir, s.runWorkerLoop)
		for _, item := range archives {
			subdir := path.Dir(item.entryPath)
			if subdir == "." {
				subdir = ""
			}
			skill, err := domain.createSkillFromArchive(userID, item.archive, req, sourceType, "", "", subdir, "", false)
			if err != nil {
				return err
			}
			result.Skills = append(result.Skills, *skill)
		}
		return nil
	})
	if err != nil {
		for _, skill := range result.Skills {
			// Unknown commit outcomes retain archives so committed Skills stay readable.
			if _, lookupErr := s.repo.SkillVersion(skill.VersionID); errors.Is(lookupErr, gorm.ErrRecordNotFound) {
				filename := filepath.Join(s.dataDir, "skill-packages", skill.SkillID, skill.VersionID+".zip")
				if removeErr := os.Remove(filename); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					log.Printf("清理未安装技能包失败: %s: %v", skill.SkillID, removeErr)
				}
			}
		}
		return nil, err
	}
	if len(result.Skills) == 1 {
		result.Skill = &result.Skills[0]
	}
	return result, nil
}
