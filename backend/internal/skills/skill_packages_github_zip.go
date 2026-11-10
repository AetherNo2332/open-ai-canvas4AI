package skills

import (
	"archive/zip"
	"os"
	"strings"
	"yingce/backend/internal/kernel"
)

func archiveFromGitHubZip(reader *zip.Reader) (skillPackageArchive, error) {
	if len(reader.File) > maxGitHubRepositoryEntries {
		return skillPackageArchive{}, kernel.BadAuthRequest("GitHub 仓库文件数量过多")
	}
	wrapper, paths, err := gitHubZipPaths(reader)
	if err != nil {
		return skillPackageArchive{}, err
	}
	root, err := selectGitHubSkillRoot(paths)
	if err != nil {
		return skillPackageArchive{}, err
	}
	omitRootExamples := root == "" && gitHubZipExceedsPackageLimit(reader)
	files := make(map[string][]byte)
	var total int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		entryPath, err := normalizeSkillPath(entry.Name)
		if err != nil {
			return skillPackageArchive{}, err
		}
		if !strings.HasPrefix(entryPath, wrapper) {
			return skillPackageArchive{}, kernel.BadAuthRequest("GitHub 仓库 ZIP 路径异常")
		}
		relative := strings.TrimPrefix(entryPath, wrapper)
		if root != "" {
			if !strings.HasPrefix(relative, root+"/") {
				continue
			}
			relative = strings.TrimPrefix(relative, root+"/")
		} else if omitRootExamples && isRootExamplePath(relative) {
			continue
		}
		if err := validateGitHubZipFile(entry); err != nil {
			return skillPackageArchive{}, err
		}
		if _, exists := files[relative]; exists {
			return skillPackageArchive{}, kernel.BadAuthRequest("技能包包含重复文件路径")
		}
		if entry.UncompressedSize64 > maxSkillFileBytes {
			return skillPackageArchive{}, kernel.BadAuthRequest("技能包中单个文件不能超过 8MB")
		}
		file, err := entry.Open()
		if err != nil {
			return skillPackageArchive{}, err
		}
		content, err := readGitHubSkillContent(file)
		if err != nil {
			return skillPackageArchive{}, err
		}
		total += int64(len(content))
		if total > maxSkillPackageBytes {
			return skillPackageArchive{}, kernel.BadAuthRequest("技能包解压后不能超过 20MB")
		}
		files[relative] = content
		if len(files) > maxSkillPackageFiles {
			return skillPackageArchive{}, kernel.BadAuthRequest("技能包文件数量不能超过 512 个")
		}
	}
	return finalizeSkillArchive(files, parseSkillPackageMetadata(files["SKILL.md"]))
}

func validateGitHubZipFile(entry *zip.File) error {
	if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
		return kernel.BadAuthRequest("技能包不能包含软链接或特殊文件")
	}
	return nil
}

func gitHubZipPaths(reader *zip.Reader) (string, []string, error) {
	wrapper := ""
	paths := make([]string, 0, len(reader.File))
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		entryPath, err := normalizeSkillPath(entry.Name)
		if err != nil {
			return "", nil, err
		}
		parts := strings.SplitN(entryPath, "/", 2)
		if len(parts) != 2 {
			return "", nil, kernel.BadAuthRequest("GitHub 仓库 ZIP 路径异常")
		}
		if wrapper == "" {
			wrapper = parts[0] + "/"
		}
		if !strings.HasPrefix(entryPath, wrapper) {
			return "", nil, kernel.BadAuthRequest("GitHub 仓库 ZIP 路径异常")
		}
		paths = append(paths, parts[1])
	}
	return wrapper, paths, nil
}

func gitHubZipExceedsPackageLimit(reader *zip.Reader) bool {
	var count int
	var total uint64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		count++
		if count > maxSkillPackageFiles || entry.UncompressedSize64 > maxSkillFileBytes || entry.UncompressedSize64 > maxSkillPackageBytes-total {
			return true
		}
		total += entry.UncompressedSize64
	}
	return false
}
