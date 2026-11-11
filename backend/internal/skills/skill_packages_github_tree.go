package skills

import (
	"strings"
	"yingce/backend/internal/kernel"
)

type skillTreeFile struct {
	relative string
	full     string
}

func selectGitHubSkillTreeFiles(tree *githubSkillTree, subdir string) ([]skillTreeFile, error) {
	selected := make([]skillTreeFile, 0)
	var total int64
	prefix := subdir + "/"
	paths := make([]string, 0)
	for _, entry := range tree.Tree {
		if strings.HasPrefix(entry.Path, prefix) && entry.Type != "tree" {
			paths = append(paths, strings.TrimPrefix(entry.Path, prefix))
		}
	}
	root, err := selectGitHubSkillRoot(paths)
	if err != nil {
		return nil, err
	}
	if root != "" {
		prefix += root + "/"
	}
	for _, entry := range tree.Tree {
		if !strings.HasPrefix(entry.Path, prefix) || entry.Type == "tree" {
			continue
		}
		relative, err := normalizeSkillPath(strings.TrimPrefix(entry.Path, prefix))
		if err != nil {
			return nil, err
		}
		if err := validateGitHubSkillTreeFile(entry); err != nil {
			return nil, err
		}
		total += entry.Size
		if total > maxSkillPackageBytes {
			return nil, kernel.BadAuthRequest("技能包解压后不能超过 20MB")
		}
		selected = append(selected, skillTreeFile{relative: relative, full: entry.Path})
		if len(selected) > maxSkillPackageFiles {
			return nil, kernel.BadAuthRequest("技能包文件数量不能超过 512 个")
		}
	}
	if len(selected) == 0 {
		return nil, kernel.BadAuthRequest("GitHub 技能目录不存在或为空")
	}
	return selected, nil
}
