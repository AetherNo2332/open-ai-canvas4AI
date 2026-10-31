package commands

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/canvas/capability"
)

// 拆镜脚本的提示词由服务端从画布组装：剧情、连到分镜节点的风格与要求文本、角色卡、可引用素材，
// 局部重拆时再加上被重拆的行、前后各一镜（只读）和其余镜头摘要。调用方（画布按钮或 Agent）
// 只给出目标节点、重拆范围和修改意见，不能替换这些事实。

const (
	storyboardPromptTextLimit    = 20000
	storyboardPromptSummaryRunes = 60
	storyboardPromptRequestLimit = 4000
	storyboardPromptAssetLimit   = 60
)

// StoryboardSplitPromptInput 是调用方能提供的全部输入；其余上下文都从画布读取。
type StoryboardSplitPromptInput struct {
	Request             string
	ShotCount           int
	ShotDurationSeconds int
	SkillContext        string
}

// BuildStoryboardSplitPrompt 组装拆镜提示词与模型侧输出契约。
func BuildStoryboardSplitPrompt(doc Document, nodeID string, scope StoryboardSplitScope, input StoryboardSplitPromptInput) (string, error) {
	p := &planner{registry: capability.BuiltinRegistry(), now: func() time.Time { return time.Unix(0, 0) }}
	node, _, rows, err := p.storyboardNodeFromDocument(doc, nodeID)
	if err != nil {
		return "", err
	}
	meta, _ := node["metadata"].(map[string]any)
	plot := strings.TrimSpace(firstNonEmptyText(text(meta["composerContent"]), text(meta["content"])))
	if plot == "" {
		return "", BadAuthRequest("分镜节点里还没有剧情或大纲，请先写入剧情再拆镜")
	}
	request := strings.TrimSpace(input.Request)
	if utf8.RuneCountInString(request) > storyboardPromptRequestLimit {
		return "", BadAuthRequest("修改意见过长，请精简到 4000 字以内")
	}
	sections := []string{"【剧情】\n" + clipRunes(plot, storyboardPromptTextLimit)}
	if requirements := storyboardConnectedTexts(doc, nodeID); len(requirements) > 0 {
		sections = append(sections, "【项目风格与用户要求】\n"+strings.Join(requirements, "\n\n"))
	}
	if characters := storyboardCharacterLines(doc); len(characters) > 0 {
		sections = append(sections, "【角色卡】\n"+strings.Join(characters, "\n"))
	}
	if assets := storyboardAssetLines(doc, nodeID); len(assets) > 0 {
		sections = append(sections, "【可引用的画布素材】（assetRefs.nodeId 只能取这些 ID）\n"+strings.Join(assets, "\n"))
	}
	if strings.TrimSpace(input.SkillContext) != "" {
		sections = append(sections, "【写作方法】（只提供方法，不改变剧情与输出结构）\n"+clipRunes(input.SkillContext, storyboardPromptTextLimit))
	}
	requirement := []string{"每个镜头必须能独立用于生成首帧图片和镜头视频。"}
	if len(scope.RowIDs) == 0 {
		if locked := storyboardLockedLines(rows); len(locked) > 0 {
			sections = append(sections, "【已锁定的镜头】（保持不变，不要输出它们；新镜头会排在它们周围）\n"+strings.Join(locked, "\n"))
		}
		if input.ShotCount > 0 {
			requirement = append(requirement, fmt.Sprintf("全片共 %d 个镜头（不含已锁定镜头）", input.ShotCount))
		}
		if request != "" {
			sections = append(sections, "【用户本次要求】\n"+request)
		}
	} else {
		indexes, err := storyboardScopeIndexes(rows, scope.RowIDs)
		if err != nil {
			return "", err
		}
		first, last := indexes[0], indexes[len(indexes)-1]
		sections = append(sections, storyboardResplitSections(rows, first, last, request)...)
		requirement = append(requirement, "只输出替换这一段的新镜头，镜头数量可以与原来不同；不要输出范围外的镜头")
	}
	if input.ShotDurationSeconds > 0 {
		requirement = append(requirement, fmt.Sprintf("每镜约 %d 秒，durationSeconds 填写该值", input.ShotDurationSeconds))
	}
	sections = append(sections, storyboardSplitOutputContract(strings.Join(requirement, "；")))
	return strings.Join(sections, "\n\n"), nil
}

// storyboardResplitSections 给局部重拆提供衔接上下文：被重拆的原镜头、前后各一镜（只读）与其余镜头摘要。
func storyboardResplitSections(rows []map[string]any, first, last int, request string) []string {
	sections := []string{}
	summary := []string{}
	for index, row := range rows {
		if index >= first && index <= last {
			continue
		}
		summary = append(summary, fmt.Sprintf("镜头%d：%s", index+1, clipRunes(text(row["plotDescription"]), storyboardPromptSummaryRunes)))
	}
	if len(summary) > 0 {
		sections = append(sections, "【其余镜头摘要】（保持不变）\n"+strings.Join(summary, "\n"))
	}
	if first > 0 {
		sections = append(sections, "【前一镜】（只读，新的第一镜要从它的结束状态接上）\n"+storyboardRowDetail(rows[first-1], first))
	} else {
		sections = append(sections, "【前一镜】无，这一段是全片开头。")
	}
	original := []string{}
	for index := first; index <= last; index++ {
		original = append(original, storyboardRowDetail(rows[index], index+1))
	}
	sections = append(sections, fmt.Sprintf("【要重拆的镜头】（镜头%d 至 %d）\n%s", first+1, last+1, strings.Join(original, "\n\n")))
	if last+1 < len(rows) {
		sections = append(sections, "【后一镜】（只读，新的最后一镜的 continuityOut 要能接到它的开头；不要修改它）\n"+storyboardRowDetail(rows[last+1], last+2))
	} else {
		sections = append(sections, "【后一镜】无，这一段是全片结尾。")
	}
	if request == "" {
		request = "按剧情重新组织这一段的镜头，衔接前后镜头。"
	}
	sections = append(sections, "【修改意见】\n"+request)
	return sections
}

func storyboardRowDetail(row map[string]any, shotNumber int) string {
	parts := []string{fmt.Sprintf("镜头%d（%v 秒）", shotNumber, row["durationSeconds"])}
	for _, field := range []struct{ key, label string }{
		{"plotDescription", "画面"}, {"dialogue", "台词"}, {"shotSize", "景别"}, {"camera", "机位"}, {"motion", "运镜"},
		{"emotion", "情绪"}, {"lightingAndAtmosphere", "光影"}, {"continuityOut", "结束状态"},
	} {
		if value := strings.TrimSpace(text(row[field.key])); value != "" {
			parts = append(parts, field.label+"："+clipRunes(value, 600))
		}
	}
	return strings.Join(parts, "\n")
}

func storyboardLockedLines(rows []map[string]any) []string {
	lines := []string{}
	for index, row := range rows {
		if storyboardRowLocked(row) {
			lines = append(lines, fmt.Sprintf("镜头%d：%s", index+1, clipRunes(text(row["plotDescription"]), storyboardPromptSummaryRunes)))
		}
	}
	return lines
}

// storyboardConnectedTexts 读取连到分镜节点的文本节点（项目风格、用户要求等）。
func storyboardConnectedTexts(doc Document, nodeID string) []string {
	nodes := map[string]map[string]any{}
	for _, node := range maps(doc["nodes"]) {
		nodes[text(node["id"])] = node
	}
	seen := map[string]bool{}
	texts := []string{}
	for _, edge := range maps(doc["connections"]) {
		if text(edge["toNodeId"]) != nodeID || text(edge["storyboardRowId"]) != "" {
			continue
		}
		source := nodes[text(edge["fromNodeId"])]
		id := text(source["id"])
		if source == nil || seen[id] || text(source["type"]) != "text" {
			continue
		}
		seen[id] = true
		meta, _ := source["metadata"].(map[string]any)
		body := strings.TrimSpace(firstNonEmptyText(text(meta["content"]), text(meta["prompt"])))
		if body == "" {
			continue
		}
		title := strings.TrimSpace(text(source["title"]))
		if title != "" {
			body = "《" + clipRunes(title, 80) + "》\n" + body
		}
		texts = append(texts, clipRunes(body, storyboardPromptTextLimit/4))
	}
	return texts
}

func storyboardCharacterLines(doc Document) []string {
	lines := []string{}
	for _, node := range maps(doc["nodes"]) {
		meta, _ := node["metadata"].(map[string]any)
		if text(meta["workflowKind"]) != "character" || strings.TrimSpace(text(meta["characterAssetId"])) == "" {
			continue
		}
		name := strings.TrimSpace(firstNonEmptyText(text(meta["characterName"]), text(node["title"])))
		definition := ""
		if raw, ok := meta["characterDefinition"]; ok {
			encoded, _ := json.Marshal(raw)
			definition = string(encoded)
		}
		if definition == "" || definition == "null" {
			definition = text(meta["characterPrompt"])
		}
		lines = append(lines, fmt.Sprintf("- %s（assetId=%s）：%s", name, text(meta["characterAssetId"]), clipRunes(definition, 600)))
	}
	return lines
}

func storyboardAssetLines(doc Document, nodeID string) []string {
	lines := []string{}
	for _, node := range maps(doc["nodes"]) {
		id := text(node["id"])
		kind := text(node["type"])
		if id == nodeID || (kind != "image" && kind != "drawing" && kind != "video" && kind != "audio") {
			continue
		}
		meta, _ := node["metadata"].(map[string]any)
		if strings.TrimSpace(text(meta["storageKey"])) == "" && strings.TrimSpace(text(meta["assetId"])) == "" {
			continue
		}
		category := strings.TrimSpace(text(meta["assetCategory"]))
		if text(meta["workflowKind"]) == "character" {
			category = "character"
		}
		line := fmt.Sprintf("- nodeId=%s 类型=%s 标题=%s", id, kind, clipRunes(text(node["title"]), 80))
		if category != "" {
			line += " 用途=" + category
		}
		lines = append(lines, line)
		if len(lines) >= storyboardPromptAssetLimit {
			break
		}
	}
	return lines
}

// storyboardSplitOutputContract 与 storyboardRowsFromPayload 读取的字段一一对应。
func storyboardSplitOutputContract(requirement string) string {
	return strings.Join([]string{
		"【受保护输出契约】",
		"- 只返回一个 JSON 对象，不要 Markdown、表格、代码块、注释或任何解释文字。",
		`- 顶层固定为 {"title": string, "rows": object[]}，rows 至少 1 项。` + requirement,
		"- rows 每项对应一个镜头，按剧情顺序排列；字段缺失时填空字符串或空数组，不要省略字段：",
		"  durationSeconds（整数，1-60）、plotDescription（本镜可见的剧情与关键动作）、",
		"  dialogue（本镜实际念出的台词或简短旁白，超长必须拆镜）、",
		"  narrativeIntent、viewerPOV、performanceBlocking、shotSize（景别）、emotion、lightingAndAtmosphere、",
		"  audioEffects、camera、motion、timeBeats、imageGenerationPrompt（首帧画面提示词）、",
		"  videoMotionPrompt（运镜与结尾状态）、continuityOut（本镜结束时人物位置、朝向、手中道具与光线）、negativePrompt、",
		"  mustHave（数组，最多 3 项）、optionalDetails（数组）、",
		"  assetBindings（数组，每项 {nodeId, role, priority}；nodeId 只能取【可引用的画布素材】里的 ID，role 取 character、environment、wardrobe、prop、weapon、style、motion、audio）。",
		"- 保持剧情事实、人物名称和台词原意；没有用户要求时不新增或删减情节。",
		"- 镜头数量以完整覆盖剧情因果、人物关系和关键台词为准，不要为压缩数量删减关键情节。",
	}, "\n")
}

func clipRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
