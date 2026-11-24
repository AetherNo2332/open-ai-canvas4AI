package app

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	builtinAssets "yingce/backend/builtin"
)

var (
	previsSkillJSONBlock = regexp.MustCompile("(?s)```json\r?\n(.*?)```")
	previsSkillInlineOp  = regexp.MustCompile("`(\\{\"type\":.*?\\})`")
	previsSkillSnakeWord = regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)*\b`)
	previsSkillLink      = regexp.MustCompile(`\]\((references/[^)]+)\)`)
)

// 世界引擎说明书属于内置 Skill，而不是 director 参数、场景目录或 system prompt。
func TestPrevisWorldGuideIsASelfContainedSkill(t *testing.T) {
	entry, err := builtinAssets.FS.ReadFile("previs-world-guide/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(entry)
	for _, want := range []string{"skillId: yingce-previs-world-guide", "references/paths.md", "references/moves.md", "references/edit.md", "director"} {
		if !strings.Contains(text, want) {
			t.Fatalf("SKILL.md missing %q", want)
		}
	}
	for _, banned := range []string{"previs_scene_read", "previs_scene_create", "previs_apply_patch", "previs_preview", "manual="} {
		if strings.Contains(text, banned) {
			t.Fatalf("SKILL.md must not mention legacy bypass %q", banned)
		}
	}
	for _, match := range previsSkillLink.FindAllStringSubmatch(text, -1) {
		file := path.Clean(match[1])
		if _, err := builtinAssets.FS.ReadFile("previs-world-guide/" + file); err != nil {
			t.Fatalf("SKILL.md links missing file %s: %v", file, err)
		}
	}
	if _, err := builtinAssets.FS.ReadFile("previs-world-guide/references/rules.md"); err != nil {
		t.Fatal(err)
	}
}

// 说明书里的示例必须是运行时真的接受的写法：字段名、操作类型、手法、动作、布景模板和道具 ID 都要存在。
func TestPrevisWorldGuideExamplesMatchTools(t *testing.T) {
	schema := cloudAgentPrevisPatchOperationSchema()["properties"].(map[string]any)
	operationTypes := schema["type"].(map[string]any)["enum"].([]string)
	techniquePrefix := regexp.MustCompile(`^(push|pull|dolly|zoom|pan|tilt|whip|truck|crane|orbit|pedestal)_`)
	decoded := 0
	err := fs.WalkDir(builtinAssets.FS, "previs-world-guide", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(filePath, ".md") {
			return walkErr
		}
		body, err := builtinAssets.FS.ReadFile(filePath)
		if err != nil {
			return err
		}
		content := string(body)
		for _, banned := range []string{"previs_scene_read", "previs_scene_create", "previs_apply_patch", "previs_preview", "manual="} {
			if strings.Contains(content, banned) {
				t.Errorf("%s must not mention legacy bypass %q", filePath, banned)
			}
		}
		var raws []string
		for _, match := range previsSkillJSONBlock.FindAllStringSubmatch(content, -1) {
			raws = append(raws, match[1])
		}
		for _, match := range previsSkillInlineOp.FindAllStringSubmatch(content, -1) {
			raws = append(raws, match[1])
		}
		for _, raw := range raws {
			raw = strings.TrimSpace(raw)
			if !strings.HasPrefix(raw, "[") {
				raw = "[" + raw + "]"
			}
			decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
			decoder.DisallowUnknownFields()
			var operations []cloudAgentPrevisPatchOperation
			if err := decoder.Decode(&operations); err != nil {
				t.Errorf("%s example does not decode as patch operations: %v\n%s", filePath, err, raw)
				continue
			}
			decoded += len(operations)
			for _, operation := range operations {
				if !slices.Contains(operationTypes, operation.Type) {
					t.Errorf("%s uses unknown operation %q", filePath, operation.Type)
				}
				if operation.Technique != "" {
					if _, ok := cloudAgentPrevisFindCameraTechnique(operation.Technique); !ok {
						t.Errorf("%s uses unknown technique %q", filePath, operation.Technique)
					}
				}
				if operation.LayoutTemplate != "" && !slices.Contains(cloudAgentPrevisLayoutIDs, operation.LayoutTemplate) {
					t.Errorf("%s uses unknown layout %q", filePath, operation.LayoutTemplate)
				}
				if operation.PropID != "" {
					if _, ok := cloudAgentPrevisFindProp(operation.PropID); !ok {
						t.Errorf("%s uses unknown prop %q", filePath, operation.PropID)
					}
				}
				if operation.TravelStyle != "" && !slices.Contains(cloudAgentPrevisTravelStyles, operation.TravelStyle) {
					t.Errorf("%s uses unknown travel style %q", filePath, operation.TravelStyle)
				}
				if operation.Weather != "" && !slices.Contains(cloudAgentPrevisWeathers, operation.Weather) {
					t.Errorf("%s uses unknown weather %q", filePath, operation.Weather)
				}
			}
		}
		for _, word := range previsSkillSnakeWord.FindAllString(content, -1) {
			if !techniquePrefix.MatchString(word) {
				continue
			}
			if _, ok := cloudAgentPrevisFindCameraTechnique(word); !ok {
				t.Errorf("%s mentions unknown camera technique %q", filePath, word)
			}
		}
		for _, word := range previsSkillSnakeWord.FindAllString(content, -1) {
			if strings.HasPrefix(word, "ancient_") || strings.HasPrefix(word, "modern_") {
				_, isProp := cloudAgentPrevisFindProp(word)
				if !slices.Contains(cloudAgentPrevisLayoutIDs, word) && !isProp {
					t.Errorf("%s mentions unknown layout or prop %q", filePath, word)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if decoded < 19 {
		t.Fatalf("Skill examples were not found, decoded %d operations", decoded)
	}
}

// 说明书内容只能由原生 Skill 文件读取；director 的 schema 和 read 回执都不再携带 manual 旁路。
// cameraRecipes 是 camera_move.recipe 的机器目录，不包含 Skill 正文或规则。
func TestDirectorDoesNotExposeManualBypass(t *testing.T) {
	properties := cloudAgentDirectorToolSchema(false)
	if _, ok := properties["manual"]; ok {
		t.Fatal("director schema still exposes manual")
	}
	catalog := cloudAgentPrevisCatalogGuide()
	for _, key := range []string{"manual", "rules", "cameraMoves"} {
		if _, ok := catalog[key]; ok {
			t.Fatalf("scene catalog still exposes manual bypass key %q", key)
		}
	}
	for _, key := range []string{"props", "layouts", "params", "cameraRecipes"} {
		if _, ok := catalog[key]; !ok {
			t.Fatalf("scene catalog is missing directory key %q", key)
		}
	}
	if len(catalog) != 4 {
		t.Fatalf("scene catalog must contain only directory facts, got %#v", catalog)
	}
}
