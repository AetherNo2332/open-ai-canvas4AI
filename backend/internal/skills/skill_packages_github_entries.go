package skills

import (
	"io"
	"yingce/backend/internal/kernel"
)

func gitHubTreeHasPath(tree *githubSkillTree, filePath string) bool {
	for _, entry := range tree.Tree {
		if entry.Path == filePath {
			return true
		}
	}
	return false
}

func validateGitHubSkillTreeEntry(entry githubSkillTreeEntry) error {
	normalized, err := normalizeSkillPath(entry.Path)
	if err != nil {
		return err
	}
	if normalized != entry.Path {
		return kernel.BadAuthRequest("GitHub 技能目录路径无效")
	}
	return validateGitHubSkillTreeFile(entry)
}

func validateGitHubSkillTreeFile(entry githubSkillTreeEntry) error {
	if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
		return kernel.BadAuthRequest("技能包不能包含软链接或特殊文件")
	}
	if entry.Size < 0 || entry.Size > maxSkillFileBytes {
		return kernel.BadAuthRequest("技能包中单个文件不能超过 8MB")
	}
	return nil
}

func readGitHubSkillContent(body io.ReadCloser) ([]byte, error) {
	content, readErr := io.ReadAll(io.LimitReader(body, maxSkillFileBytes+1))
	closeErr := body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(content) > maxSkillFileBytes {
		return nil, kernel.BadAuthRequest("技能包中单个文件不能超过 8MB")
	}
	return content, nil
}
