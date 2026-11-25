package shortplay

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

func TestExtractPromptCarriesAssetListAndBatch(t *testing.T) {
	settings := production.Settings{AspectRatio: "9:16", Language: "中文", StylePrompt: "霓虹雨夜"}
	batch := []model.ShortplayEpisode{
		{Number: 1, Title: "雨夜", ScriptText: "沈青禾撑伞站在巷口。"},
		{Number: 2, Title: "回眸", ScriptText: "她回头看了一眼。"},
	}
	hints := []AssetHint{{ID: 2, Kind: ExtractKindCharacter, Name: "沈青禾", Aliases: []string{"青禾", " "}}}
	prompt := ExtractPrompt("雨夜回眸", settings, batch, 2, hints, nil)
	for _, want := range []string{
		"作品：雨夜回眸",
		"画幅：9:16",
		"对白语言：中文",
		"视觉风格：霓虹雨夜",
		"本批覆盖第 1–2 集，全剧共 2 集",
		"【当前资产清单】",
		`[{"assetId":2,"kind":"character","name":"沈青禾","aliases":["青禾"]}]`,
		"## 第 1 集 · 雨夜",
		"## 第 2 集 · 回眸",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, ExtractSystemPrompt) {
		t.Fatal("system prompt must stay out of the user prompt body")
	}
	if got := ExtractPrompt("空批次", settings, nil, 0, nil, nil); got != "" {
		t.Fatalf("empty batch prompt = %q", got)
	}
	first := ExtractPrompt("首批", settings, batch[:1], 2, nil, nil)
	if !strings.Contains(first, "【当前资产清单】\n\n[]\n") {
		t.Fatalf("empty hint list must render as []: %s", first)
	}
}

func TestExtractPromptIncludesTruncatedSkill(t *testing.T) {
	long := strings.Repeat("技能正文", production.MaxWritingSkillRunes)
	prompt := ExtractPrompt("技能", production.Settings{}, []model.ShortplayEpisode{{Number: 1, Title: "一", ScriptText: "正文"}}, 1, nil,
		&production.WritingSkill{Name: "雨夜写作法", Path: "skills/rain.md", Content: long})
	if !strings.Contains(prompt, "【写作技能：雨夜写作法】") || !strings.Contains(prompt, "skills/rain.md") {
		t.Fatalf("skill block missing: %s", prompt)
	}
	if strings.Count(prompt, "技能正文") > production.MaxWritingSkillRunes {
		t.Fatal("skill content was not truncated")
	}
}

func TestParseExtractAssets(t *testing.T) {
	raw := "```json\n" + `{
		"characters":[{"name":" 沈青禾 ","aliases":["青禾"," "],"description":"二十岁女性","appearance":"齐肩黑发","voice":"青年女声"},{"name":"沈青禾","description":"重复条目"}],
		"props":[{"name":"油纸伞","assetId":3,"appearance":"深青油纸"}],
		"scenes":[{"name":"","appearance":"空名应丢弃"},{"name":"雨巷","assetId":1}]
	}` + "\n```"
	assets, err := ParseExtractAssets(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 3 {
		t.Fatalf("assets = %#v", assets)
	}
	if assets[0].Kind != ExtractKindCharacter || assets[0].Name != "沈青禾" || assets[0].AssetID != 0 {
		t.Fatalf("character = %#v", assets[0])
	}
	if len(assets[0].Aliases) != 1 || assets[0].Aliases[0] != "青禾" {
		t.Fatalf("aliases = %#v", assets[0].Aliases)
	}
	if assets[0].Description != "二十岁女性" || assets[0].Voice != "青年女声" {
		t.Fatalf("fields trimmed wrong: %#v", assets[0])
	}
	if assets[1].Kind != ExtractKindProp || assets[1].AssetID != 3 {
		t.Fatalf("prop = %#v", assets[1])
	}
	if assets[2].Kind != ExtractKindScene || assets[2].Name != "雨巷" || assets[2].AssetID != 1 {
		t.Fatalf("scene = %#v", assets[2])
	}
}

func TestParseExtractAssetsRejectsInvalidReplies(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":         "",
		"not-json":      "角色：沈青禾",
		"no-characters": `{"characters":[],"props":[{"name":"伞"}]}`,
		"empty-object":  `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseExtractAssets(raw); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseExtractAssetsTruncatesAndDropsInvalidIDs(t *testing.T) {
	var items strings.Builder
	for i := 0; i < MaxExtractCharacters+3; i++ {
		if items.Len() > 0 {
			items.WriteString(",")
		}
		items.WriteString(`{"name":"角色`)
		items.WriteString(strings.Repeat("甲", i+1))
		items.WriteString(`"}`)
	}
	raw := `{"characters":[` + items.String() + `,{"name":"负 id","assetId":-1}],"props":[{"assetId":-2,"name":"坏道具"}]}`
	assets, err := ParseExtractAssets(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != MaxExtractCharacters {
		t.Fatalf("kept %d, want %d", len(assets), MaxExtractCharacters)
	}
	for _, asset := range assets {
		if asset.AssetID < 0 {
			t.Fatalf("negative asset id kept: %#v", asset)
		}
	}
}
