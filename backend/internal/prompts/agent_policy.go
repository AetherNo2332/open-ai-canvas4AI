package prompts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed agent-system-policy.md agent-media-policy.md
var policyFiles embed.FS

type Policy struct {
	ID      string
	Version int
	Text    string
	Hash    string
}

func LoadAgentPolicies() (system Policy, media Policy, err error) {
	system, err = loadPolicy("agent-system-policy.md")
	if err != nil {
		return Policy{}, Policy{}, err
	}
	media, err = loadPolicy("agent-media-policy.md")
	if err != nil {
		return Policy{}, Policy{}, err
	}
	if system.ID != "cloud-agent-system" || media.ID != "cloud-agent-media" {
		return Policy{}, Policy{}, fmt.Errorf("agent policy identities are invalid")
	}
	// Harness 装配已交给 agent/ 里的 Node worker（按 pi.dev 约定读
	// SYSTEM.md / APPEND_SYSTEM.md / AGENTS.md / SOUL.md / TOOLS.md），因此 Go 侧
	// **不再把 Harness 正文追加进系统提示**（否则同一份文件会出现两次）。
	//
	// 但"本轮用的是哪一版 Harness"属于运行合同的一部分：如果不纳入哈希，改动
	// Harness 后旧运行仍会被判为合同未变并继续续聊，等于丢失合同快照。这里只把
	// Harness 内容**折进哈希**，不并入 system.Text —— 正文装配权仍归 Node。
	workspace, err := loadAgentWorkspace(os.Getenv("CANVAS_AGENT_HARNESS_DIR"))
	if err != nil {
		return Policy{}, Policy{}, err
	}
	if workspace != "" {
		system.Hash = policyHash(system.ID, system.Version, system.Text+"\n\n"+workspace)
	}
	return system, media, nil
}

// Workspace documents supply operator instructions, never tool permissions.
// The server's embedded policy and tool registry remain authoritative.
func loadAgentWorkspace(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("Agent harness directory is empty")
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("Agent harness directory must be an absolute path")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("stat Agent harness directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("Agent harness path must be a directory without symlinks")
	}
	projectFile := "AGENTS.md"
	if _, err := os.Lstat(filepath.Join(dir, projectFile)); os.IsNotExist(err) {
		projectFile = "AGENT.md"
	} else if err != nil {
		return "", fmt.Errorf("stat Agent harness %s: %w", projectFile, err)
	}
	var sections []string
	hasProject := false
	for _, name := range []string{"SOUL.md", projectFile, "TOOLS.md"} {
		path := filepath.Join(dir, name)
		entry, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("stat Agent harness %s: %w", name, err)
		}
		if !entry.Mode().IsRegular() || entry.Size() > 20*1024 {
			return "", fmt.Errorf("Agent harness %s must be a regular file of at most 20 KiB", name)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read Agent harness %s: %w", name, err)
		}
		if len(content) > 20*1024 || !utf8.Valid(content) || strings.TrimSpace(string(content)) == "" {
			return "", fmt.Errorf("Agent harness %s must contain nonempty UTF-8 text", name)
		}
		if name == projectFile {
			hasProject = true
		}
		sections = append(sections, "## Workspace "+name+"\n"+strings.TrimSpace(string(content)))
	}
	if !hasProject {
		return "", fmt.Errorf("Agent harness directory requires AGENTS.md or AGENT.md")
	}
	return strings.Join(sections, "\n\n"), nil
}

func loadPolicy(path string) (Policy, error) {
	data, err := policyFiles.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read agent policy %s: %w", path, err)
	}
	id, version, text, err := parsePolicyDocument(string(data))
	if err != nil {
		return Policy{}, fmt.Errorf("parse agent policy %s: %w", path, err)
	}
	return Policy{ID: id, Version: version, Text: text, Hash: policyHash(id, version, text)}, nil
}

func policyHash(id string, version int, text string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("id:%s\nversion:%d\n%s", id, version, text)))
	return hex.EncodeToString(sum[:])
}

func parsePolicyDocument(raw string) (string, int, string, error) {
	raw = strings.TrimSpace(raw)
	lines := strings.Split(raw, "\n")
	if len(lines) < 4 || strings.TrimSpace(lines[0]) != "---" {
		return "", 0, "", fmt.Errorf("missing metadata header")
	}
	end := -1
	metadata := map[string]string{}
	for index := 1; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "---" {
			end = index
			break
		}
		key, value, ok := strings.Cut(line, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return "", 0, "", fmt.Errorf("invalid metadata line %q", line)
		}
		if key != "id" && key != "version" {
			return "", 0, "", fmt.Errorf("unsupported metadata field %q", key)
		}
		if _, exists := metadata[key]; exists {
			return "", 0, "", fmt.Errorf("duplicate metadata field %q", key)
		}
		metadata[key] = value
	}
	if end < 0 {
		return "", 0, "", fmt.Errorf("metadata header is not closed")
	}
	id := metadata["id"]
	if id == "" {
		return "", 0, "", fmt.Errorf("policy id is required")
	}
	version, err := strconv.Atoi(metadata["version"])
	if err != nil || version <= 0 {
		return "", 0, "", fmt.Errorf("policy version must be a positive integer")
	}
	text := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if text == "" {
		return "", 0, "", fmt.Errorf("policy body is empty")
	}
	return id, version, text, nil
}
