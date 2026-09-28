package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var nativeSkillPromptCWD = regexp.MustCompile(`<cwd>\n([^\r\n]+)\n</cwd>`)
var nativeSkillWorkerCWD = regexp.MustCompile(`^(?:/|[A-Za-z]:/).*/canvas-pi-run-[^/]+/cwd$`)

// Pi includes its worker-local cwd and Skill locations in the prompt. Only
// these generated locations are variable across restarts; all prose stays frozen.
func cloudAgentAssembledPromptIdentity(state *cloudAgentRuntime, prompt string) string {
	if state.SkillRuntimeMode == cloudAgentSkillRuntimeNative {
		prompt = nativeSkillPromptIdentity(prompt, state.Skills)
	}
	return strings.TrimSpace(prompt)
}

func nativeSkillPromptIdentity(prompt string, skills []cloudAgentSkill) string {
	matches := nativeSkillPromptCWD.FindAllStringSubmatchIndex(prompt, -1)
	if len(matches) == 0 {
		return prompt
	}
	match := matches[len(matches)-1]
	cwd := strings.ReplaceAll(prompt[match[2]:match[3]], "\\", "/")
	if !nativeSkillWorkerCWD.MatchString(cwd) {
		return prompt
	}
	prompt = prompt[:match[2]] + "canvas-agent-run" + prompt[match[3]:]
	for _, skill := range skills {
		location := cwd + "/skills/" + skill.NativeName + "/SKILL.md"
		for _, candidate := range []string{location, strings.ReplaceAll(location, "/", "\\")} {
			escaped := strings.ReplaceAll(html.EscapeString(candidate), "&#39;", "&apos;")
			escaped = strings.ReplaceAll(escaped, "&#34;", "&quot;")
			prompt = strings.ReplaceAll(prompt, "<location>"+escaped+"</location>", "<location>skills/"+skill.NativeName+"/SKILL.md</location>")
		}
	}
	return prompt
}

const (
	cloudAgentSkillRuntimeLegacy  = "legacy-go"
	cloudAgentSkillRuntimeNative  = "pi-native"
	piNativeSkillNameMaxLength    = 64
	piNativeSkillDescriptionMax   = 500
	piNativeSkillReadMaxRunes     = 12_000
	piNativeSkillReadDefaultLimit = 12_000
)

// PiSkillFile is the immutable, non-secret file metadata exposed to the Pi
// worker. File content is deliberately fetched through the lease-fenced bridge.
type PiSkillFile struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType,omitempty"`
	Text     bool   `json:"text"`
}

// PiSkillSnapshot is the native view of one selected Skill. The Go Skill ID,
// version ID and content hash are the authorization identity; nativeName is
// only a stable Pi directory name.
type PiSkillSnapshot struct {
	ID           string        `json:"id"`
	NativeName   string        `json:"nativeName"`
	DisplayName  string        `json:"displayName"`
	Description  string        `json:"description"`
	VersionID    string        `json:"versionId"`
	Version      string        `json:"version"`
	ContentHash  string        `json:"contentHash"`
	EntryPath    string        `json:"entryPath"`
	Files        []PiSkillFile `json:"files"`
	EntryContent string        `json:"entryContent,omitempty"`
}

type PiSkillReadPage struct {
	NativeName  string `json:"nativeName"`
	SkillID     string `json:"skillId"`
	VersionID   string `json:"versionId"`
	ContentHash string `json:"contentHash"`
	SHA256      string `json:"sha256"`
	IsEntry     bool   `json:"isEntry"`
	Path        string `json:"path"`
	Offset      int    `json:"offset"`
	Limit       int    `json:"limit"`
	Content     string `json:"content"`
	HasMore     bool   `json:"hasMore"`
	TotalRunes  int    `json:"totalRunes"`
}

func nativeSkillEntryDigest(skill cloudAgentSkill) (string, int64, error) {
	entry, err := normalizePiSkillEntry(skill)
	if err != nil {
		return "", 0, err
	}
	digest := sha256.Sum256([]byte(entry))
	return hex.EncodeToString(digest[:]), int64(len(entry)), nil
}

func nativeSkillName(displayName, skillID string) string {
	digest := sha256.Sum256([]byte(skillID))
	name := "skill-" + hex.EncodeToString(digest[:])[:24]
	if len(name) > piNativeSkillNameMaxLength {
		return name[:piNativeSkillNameMaxLength]
	}
	return name
}

func normalizePiSkillDescription(description, fallback string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		description = strings.TrimSpace(fallback)
	}
	if description == "" {
		description = "Use this skill when the task matches its workflow."
	}
	if utf8.RuneCountInString(description) > piNativeSkillDescriptionMax {
		runes := []rune(description)
		description = strings.TrimSpace(string(runes[:piNativeSkillDescriptionMax])) + "…"
	}
	return strings.ReplaceAll(strings.ReplaceAll(description, "\r", " "), "\n", " ")
}

func normalizePiSkillEntry(skill cloudAgentSkill) (string, error) {
	name := strings.TrimSpace(skill.NativeName)
	if name == "" {
		name = nativeSkillName(skill.Name, skill.ID)
	}
	if len(name) == 0 || len(name) > piNativeSkillNameMaxLength {
		return "", fmt.Errorf("invalid native Skill name")
	}
	description := normalizePiSkillDescription(skill.Description, skill.Name)
	body := skill.Instruction
	if first, rest, ok := strings.Cut(body, "\n"); ok && strings.TrimSuffix(first, "\r") == "---" {
		for {
			line, remaining, hasNewline := strings.Cut(rest, "\n")
			if strings.TrimSuffix(line, "\r") == "---" {
				body = remaining
				break
			}
			if !hasNewline { break }
			rest = remaining
		}
	}
	if !utf8.ValidString(body) {
		return "", fmt.Errorf("native Skill entry is not UTF-8")
	}
	return "---\nname: " + strconv.Quote(name) + "\ndescription: " + strconv.Quote(description) + "\n---\n" + body, nil
}

func makePiSkillSnapshot(skill cloudAgentSkill) (PiSkillSnapshot, error) {
	nativeName := strings.TrimSpace(skill.NativeName)
	if nativeName == "" {
		nativeName = nativeSkillName(skill.Name, skill.ID)
	}
	files := append([]PiSkillFile(nil), skill.NativeFiles...)
	if len(files) == 0 {
		for path, hash := range skill.Files {
			if isNativeSkillTextPath(path) {
				files = append(files, PiSkillFile{Path: path, SHA256: hash, Text: true})
			}
		}
	}
	if len(files) == 0 && strings.TrimSpace(skill.Instruction) != "" {
		files = []PiSkillFile{{Path: cloudAgentSkillEntryPath, Text: true}}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return PiSkillSnapshot{
		ID: skill.ID, NativeName: nativeName, DisplayName: skill.Name,
		Description: normalizePiSkillDescription(skill.Description, skill.Name),
		VersionID:   firstNonEmpty(skill.VersionID, skill.Version, skill.ID+":"+skill.Hash),
		Version:     firstNonEmpty(skill.VersionLabel, skill.Version), ContentHash: skill.Hash,
		EntryPath: cloudAgentSkillEntryPath, Files: files,
	}, nil
}

func isNativeSkillTextPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".json")
}

func normalizeNativeSkillReadPath(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return "", BadAuthRequest("Skill file path is invalid")
	}
	clean := path.Clean(value)
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == "." || segment == "" || segment == ".git" || strings.Contains(segment, ":") {
			return "", BadAuthRequest("Skill file path escapes the Skill directory")
		}
	}
	if clean == "." || strings.HasPrefix(clean, ".git/") {
		return "", BadAuthRequest("Skill file path escapes the Skill directory")
	}
	if len(clean) > 1000 {
		return "", BadAuthRequest("Skill file path is too long")
	}
	return clean, nil
}

func nativeSkillReadPage(content string, offset, limit int) (PiSkillReadPage, error) {
	if offset < 0 || limit < 0 || limit > piNativeSkillReadMaxRunes {
		return PiSkillReadPage{}, BadAuthRequest("Skill read range is invalid")
	}
	if limit == 0 {
		limit = piNativeSkillReadDefaultLimit
	}
	runes := []rune(content)
	if offset > len(runes) {
		offset = len(runes)
	}
	end := offset + limit
	if end > len(runes) {
		end = len(runes)
	}
	return PiSkillReadPage{Offset: offset, Limit: limit, Content: string(runes[offset:end]), HasMore: end < len(runes), TotalRunes: len(runes)}, nil
}

func piSkillSnapshots(skills []cloudAgentSkill) ([]PiSkillSnapshot, error) {
	result := make([]PiSkillSnapshot, 0, len(skills))
	seen := map[string]string{}
	for _, skill := range skills {
		snapshot, err := makePiSkillSnapshot(skill)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[snapshot.NativeName]; ok {
			return nil, fmt.Errorf("native Skill name collision: %s", snapshot.NativeName)
		}
		seen[snapshot.NativeName] = snapshot.ID
		result = append(result, snapshot)
	}
	return result, nil
}
