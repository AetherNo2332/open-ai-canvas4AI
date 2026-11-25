package shortplay

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

// 提取阶段的数量上限：决定落库截断顺序，app 层写入前还会按同一组常量再兜一遍。
const (
	MaxExtractCharacters = 24
	MaxExtractProps      = 20
	MaxExtractScenes     = 16
)

// 资产分类字面量，与 shortplay_assets.kind 的落库值一致。
const (
	ExtractKindCharacter = "character"
	ExtractKindProp      = "prop"
	ExtractKindScene     = "scene"
)

// AssetHint 是已提取资产在提示词里的身份行：项目内序号 + 分类 + 标准名 + 别名。
// 提取前从 shortplay_assets 现读，模型据此判断「已有 / 新增」并回带 assetId。
type AssetHint struct {
	ID      int
	Kind    string
	Name    string
	Aliases []string
}

// ExtractedAsset 是一条提取结果。AssetID 为 0 表示新资产；非 0 时必须命中本项目清单。
type ExtractedAsset struct {
	AssetID     int      `json:"assetId,omitempty"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	Description string   `json:"description,omitempty"`
	Appearance  string   `json:"appearance,omitempty"`
	Voice       string   `json:"voice,omitempty"`
}

// extractSchema 只描述角色/道具/场景三类资产身份：提取不做作品圣经。
// 解析侧复用 production.ParseBible 的同款字段名，但这里自带结构，避免把 world-view
// 字段带进短剧工作台；prompts_test 锁定了本段文案。
const extractSchema = `{"characters":[{"name":"标准名","assetId":"已有资产填清单里的整数序号；新资产省略此字段","aliases":["别名/称呼"],"description":"身份与性格；必须写明年代阶段（青年/中年/老年）与性别（男/女），非人类实体明确写「非人」","appearance":"可直接用于生图的外貌、发型、服装描述（中文，40-80字）；须呼应 description 的年代阶段与性别，非人实体写清形态","voice":"音色描述提示词，如「中年男声、沉稳低音」；非人实体写清拟声方向"}],` +
	`"props":[{"name":"道具名","assetId":"已有资产填清单里的整数序号；新资产省略此字段","description":"用途与归属","appearance":"可直接用于生图的外形、材质、颜色描述"}],` +
	`"scenes":[{"name":"场景名","assetId":"已有资产填清单里的整数序号；新资产省略此字段","description":"用途","appearance":"可直接用于生图的环境描述"}]}`

// extractAssetListTemplate 是「当前资产清单」段，{assetList} 由 ExtractPrompt 注入。
// 不用 text/template：正文里全是 JSON 花括号，{{}} 会和模板语法打架。
const extractAssetListTemplate = `【当前资产清单】

{assetList}

输出规则：
- 清单里已有的资产：原样带回它的 assetId（整数）与标准名。同名条目视为对这一行的覆盖式更新 —— 你这次写出的 description / appearance / voice 会覆盖旧值，没写或留空的字段保留旧值；aliases 只写新增别名，服务端会并入已有别名。
- 名字不同但其实是同一人物 / 物件 / 地点时，一律归并到清单里已有的那一行（带它的 assetId），不要另起新名。
- 清单里确实没有的资产：作为新条目输出，省略 assetId，字段写全。
- 不要拼装或猜测 assetId；清单里没有的序号绝不写。
- 清单是 [] 时说明这是首批提取：所有资产都作为新条目输出，省略 assetId。

`

const extractRequirements = "要求：角色按重要性排序，最多 24 个，只保留会在镜头中出现的人物；同一人物的不同称呼必须合并到 aliases；道具最多 20 个，只保留反复出现或推动剧情的关键物件；场景最多 16 个，只保留反复出现的地点。\n" +
	"description 与 appearance 必须把人物或生物的年代阶段（青年/中年/老年）与性别（男/女）写清楚，非人类实体明确标注「非人」；age 不需要精确数字。角色可另给 voice 音色描述提示词（如「中年男声、沉稳低音」）。\n" +
	"assetId 只能抄清单里的整数，不是字符串、不是名字；已有资产可以只写这次要更新的字段，新资产必须把字段写全。输出仍只能是一个 JSON 对象。\n\n"

// ExtractSystemPrompt 是提取任务的系统提示词：只做资产抽取，不做世界观或剧情改编。
const ExtractSystemPrompt = "你是短剧资产提取助手。每轮都会给你一份当前资产清单：先对照它判断哪些资产已存在、哪些是新增，再决定输出内容。阅读剧本，只抽取其中真实出现过的三类资产：角色、道具、场景。为每一项给出可用于后续生图与排期的标准名称、别名、描述与外观；不编写世界观、基调或整体画面规则，也不做剧情改编建议。只输出一个 JSON 对象，不要 Markdown 代码块，不要解释。"

// ExtractPrompt 是提取 job 的提示词：正文按批完整拼接，不抽样也不截断
// （批次已由 NextEpisodeBatch 按预算切好），清单段每轮都在（首批是空数组）。
func ExtractPrompt(title string, settings production.Settings, batch []model.ShortplayEpisode, totalEpisodes int, hints []AssetHint, skill *production.WritingSkill) string {
	if len(batch) == 0 {
		return ""
	}
	settings = settings.Normalized()
	var b strings.Builder
	fmt.Fprintf(&b, "作品：%s\n画幅：%s\n视觉风格：%s\n对白语言：%s\n集数：%d\n",
		title, settings.AspectRatio, firstNonEmpty(settings.StylePrompt, "写实电影感"), settings.Language, totalEpisodes)
	first, last := batch[0].Number, batch[len(batch)-1].Number
	fmt.Fprintf(&b, "本批覆盖第 %d–%d 集，全剧共 %d 集\n\n", first, last, totalEpisodes)
	b.WriteString(strings.ReplaceAll(extractAssetListTemplate, "{assetList}", renderAssetList(hints)))
	b.WriteString(extractSkillBlock(skill))
	b.WriteString("请输出：\n")
	b.WriteString(extractSchema + "\n")
	b.WriteString(extractRequirements)
	b.WriteString("【剧本原文】\n")
	for _, episode := range batch {
		fmt.Fprintf(&b, "## 第 %d 集 · %s\n%s\n\n", episode.Number, episode.Title, episode.ScriptText)
	}
	return b.String()
}

// extractSkillBlock 把写作技能交给提取步骤；输出结构仍以本步骤 JSON 为准。
func extractSkillBlock(skill *production.WritingSkill) string {
	if skill == nil || strings.TrimSpace(skill.Content) == "" {
		return ""
	}
	return fmt.Sprintf("【写作技能：%s】\n下面是提取角色、道具与场景必须遵循的方法与标准（来自技能文件 %s）。按技能决定取舍、粒度与写法；输出仍然只能是下面要求的 JSON，字段名与结构不变，技能与 JSON 结构冲突时以 JSON 结构为准，技能里要求的其他输出格式不要输出。\n<skill>\n%s\n</skill>\n\n",
		skill.Name, skill.Path, truncateSkillRunes(strings.TrimSpace(skill.Content), production.MaxWritingSkillRunes))
}

func truncateSkillRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}

// renderAssetList 把清单压成单行紧凑 JSON：只带 id/kind/name/aliases，省 token。
func renderAssetList(hints []AssetHint) string {
	type entry struct {
		AssetID int      `json:"assetId"`
		Kind    string   `json:"kind"`
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
	}
	items := make([]entry, 0, len(hints))
	for _, hint := range hints {
		items = append(items, entry{AssetID: hint.ID, Kind: hint.Kind, Name: hint.Name, Aliases: nonEmptyStrings(hint.Aliases)})
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// ParseExtractAssets 解析并校验提取回复：去掉 Markdown 代码块后只接受一个 JSON 对象，
// 角色不能为空；每类按上限截断并丢掉空名条目；assetId < 0 视为无效引用。
func ParseExtractAssets(raw string) ([]ExtractedAsset, error) {
	trimmed := stripCodeFence(raw)
	if trimmed == "" {
		return nil, errors.New("提取结果为空")
	}
	var reply struct {
		Characters []ExtractedAsset `json:"characters"`
		Props      []ExtractedAsset `json:"props"`
		Scenes     []ExtractedAsset `json:"scenes"`
	}
	if err := json.Unmarshal([]byte(trimmed), &reply); err != nil {
		return nil, fmt.Errorf("提取结果不是合法 JSON：%w", err)
	}
	if len(reply.Characters) == 0 {
		return nil, errors.New("提取结果缺少角色")
	}
	result := make([]ExtractedAsset, 0, len(reply.Characters)+len(reply.Props)+len(reply.Scenes))
	result = appendExtracted(result, ExtractKindCharacter, reply.Characters, MaxExtractCharacters)
	result = appendExtracted(result, ExtractKindProp, reply.Props, MaxExtractProps)
	result = appendExtracted(result, ExtractKindScene, reply.Scenes, MaxExtractScenes)
	if len(result) == 0 {
		return nil, errors.New("提取结果没有可用资产")
	}
	return result, nil
}

func appendExtracted(dst []ExtractedAsset, kind string, items []ExtractedAsset, limit int) []ExtractedAsset {
	seen := make(map[string]struct{}, len(items))
	written := 0
	for _, item := range items {
		if written >= limit {
			break
		}
		item.Kind = kind
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			continue
		}
		if item.AssetID < 0 {
			continue
		}
		item.Aliases = nonEmptyStrings(item.Aliases)
		key := strings.ToLower(item.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		item.Description = strings.TrimSpace(item.Description)
		item.Appearance = strings.TrimSpace(item.Appearance)
		item.Voice = strings.TrimSpace(item.Voice)
		dst = append(dst, item)
		written++
	}
	return dst
}

func stripCodeFence(raw string) string {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "```") {
		return value
	}
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		value = value[newline+1:]
	}
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
