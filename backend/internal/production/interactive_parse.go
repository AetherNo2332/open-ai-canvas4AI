package production

import (
	"encoding/json"
	"fmt"
	"strings"
)

// InteractiveRow 是互动剧拆分表的一行：一个可制作的剧情段落。
// 剧情总图只放文字节点，选择是节点之间带文案的连线，结局是文字节点的标记。
type InteractiveRow struct {
	// Index 行号，从 1 开始连续编号，全表唯一；行号 1 是入口节点。
	Index int `json:"index"`
	// Text 正文：本段落可拍摄的剧情内容，保留原文台词与动作顺序。
	Text string `json:"text"`
	// Characters 本段落出镜角色名列表（使用原文称呼，没有则空数组）。
	Characters []string `json:"characters"`
	// Scene 本段落发生的场景名。
	Scene string `json:"scene"`
	// Next 去向列表；结局段落必须为空数组。
	Next []InteractiveEdge `json:"next"`
	// Ending 标记本段落是结局（故事终点），结局段落不再有去向。
	Ending bool `json:"ending"`
	// Suggested 标记本段落是拆分新增的建议（原文单线故事时供编剧确认），不是原文已有内容。
	Suggested bool `json:"suggested"`
}

// InteractiveEdge 是剧情节点之间带文案的连线（玩家看到的选择）。
type InteractiveEdge struct {
	// Target 目标行号，必须指向表中已存在的行。
	Target int `json:"target"`
	// Option 选项文案；已有分支逐字保留原文，新增建议分支由模型撰写。
	Option string `json:"option"`
	// Suggested 为 true 表示这是对单线故事新增的建议去向，供编剧确认。
	Suggested bool `json:"suggested"`
}

// InteractiveCast 是待匹配名称清单：给资产匹配环节的是角色/场景名称，不是模型猜测的资产 ID。
type InteractiveCast struct {
	Characters []string `json:"characters"`
	Scenes     []string `json:"scenes"`
}

// InteractiveScript 是剧本拆分的结构化结果，可直接导入剧情总图。
type InteractiveScript struct {
	Title string           `json:"title"`
	Rows  []InteractiveRow `json:"rows"`
	Cast  InteractiveCast  `json:"cast"`
}

// InteractiveIssue 是行级校验结果，Row 定位到具体行（0 表示全局问题）。
type InteractiveIssue struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// InteractiveMaxRows 是单表行数上限，防异常输入撑爆导入。
const InteractiveMaxRows = 500

// InteractiveMaxCharacters 是单行角色名上限，超限报 too_many_characters 而不是静默截断。
const InteractiveMaxCharacters = 50

// InteractiveMaxEdges 是单行去向上限，超限报 too_many_edges 而不是静默截断。
const InteractiveMaxEdges = 20

// ParseInteractiveScript 解析拆分结果并做行级校验。
// error 只表示 JSON 结构不可读；行级问题进 issues，由调用方定位到行展示并允许修正，不落库、不写半张图。
func ParseInteractiveScript(raw string) (InteractiveScript, []InteractiveIssue, error) {
	text, err := ExtractJSONObject(raw)
	if err != nil {
		return InteractiveScript{}, nil, err
	}
	var payload struct {
		Title string           `json:"title"`
		Rows  []InteractiveRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return InteractiveScript{}, nil, fmt.Errorf("拆分结果 JSON 解析失败：%w", err)
	}
	script := InteractiveScript{Title: strings.TrimSpace(payload.Title), Rows: normalizeInteractiveRows(payload.Rows)}
	issues := ValidateInteractiveScript(script)
	if len(issues) > 0 {
		return script, issues, nil
	}
	script.Cast = interactiveCast(script.Rows)
	return script, nil, nil
}

// ValidateInteractiveScript 按固定表头规则逐行校验，问题全部定位到行。
// 超过单表行数上限时整表拒绝（只报全局问题），不截断后导入半张剧情总图。
func ValidateInteractiveScript(script InteractiveScript) []InteractiveIssue {
	if len(script.Rows) > InteractiveMaxRows {
		return []InteractiveIssue{interactiveIssue(0, "rows", "too_many_rows",
			"表格共 %d 行，超过单表上限 %d 行，请拆分为多张表后分别拆分", len(script.Rows), InteractiveMaxRows)}
	}
	return append(validateInteractiveIndexes(script.Rows), validateInteractiveFlow(script.Rows)...)
}

// validateInteractiveIndexes 校验行号、必填文本与行内字段上限，最后检查行号连续性。
// 行号超出本表行数时单独报错，跳号扫描因此可以按本表行数封顶，不会被任意大的行号拖死。
func validateInteractiveIndexes(rows []InteractiveRow) []InteractiveIssue {
	var issues []InteractiveIssue
	seen := map[int]bool{}
	maxIndex := 0
	for i := range rows {
		row := &rows[i]
		if row.Index <= 0 {
			issues = append(issues, interactiveIssue(row.Index, "index", "invalid_index", "行号必须是正整数，当前为 %d", row.Index))
			continue
		}
		if seen[row.Index] {
			issues = append(issues, interactiveIssue(row.Index, "index", "duplicate_index", "第 %d 行行号重复", row.Index))
		}
		seen[row.Index] = true
		if row.Index > maxIndex {
			maxIndex = row.Index
		}
		if row.Index > len(rows) {
			issues = append(issues, interactiveIssue(row.Index, "index", "index_out_of_range",
				"第 %d 行行号超出本表行数 %d，行号必须从 1 连续编号", row.Index, len(rows)))
		}
		if strings.TrimSpace(row.Text) == "" {
			issues = append(issues, interactiveIssue(row.Index, "text", "missing_text", "第 %d 行缺少正文", row.Index))
		}
		if strings.TrimSpace(row.Scene) == "" {
			issues = append(issues, interactiveIssue(row.Index, "scene", "missing_scene", "第 %d 行缺少场景", row.Index))
		}
		if len(row.Characters) > InteractiveMaxCharacters {
			issues = append(issues, interactiveIssue(row.Index, "characters", "too_many_characters",
				"第 %d 行角色 %d 个，超过单行上限 %d 个", row.Index, len(row.Characters), InteractiveMaxCharacters))
		}
		if len(row.Next) > InteractiveMaxEdges {
			issues = append(issues, interactiveIssue(row.Index, "next", "too_many_edges",
				"第 %d 行去向 %d 个，超过单行上限 %d 个", row.Index, len(row.Next), InteractiveMaxEdges))
		}
	}
	limit := maxIndex
	if limit > len(rows) {
		limit = len(rows)
	}
	for index := 1; index <= limit; index++ {
		if !seen[index] {
			issues = append(issues, interactiveIssue(index, "index", "missing_index", "行号不连续：缺少第 %d 行", index))
		}
	}
	return issues
}

// validateInteractiveFlow 校验结局、死路与每条去向的选项文案和目标存在性。
func validateInteractiveFlow(rows []InteractiveRow) []InteractiveIssue {
	known := map[int]bool{}
	for _, row := range rows {
		if row.Index > 0 {
			known[row.Index] = true
		}
	}
	var issues []InteractiveIssue
	endingCount := 0
	for i := range rows {
		row := &rows[i]
		if row.Index <= 0 {
			continue
		}
		if row.Ending {
			endingCount++
			if len(row.Next) > 0 {
				issues = append(issues, interactiveIssue(row.Index, "next", "ending_has_next", "第 %d 行已标记为结局，不能再有去向", row.Index))
			}
			continue
		}
		if len(row.Next) == 0 {
			issues = append(issues, interactiveIssue(row.Index, "next", "dead_end", "第 %d 行既不是结局也没有去向，剧情无法继续", row.Index))
			continue
		}
		for j := range row.Next {
			issues = append(issues, validateInteractiveEdge(row.Index, &row.Next[j], known)...)
		}
	}
	if len(rows) > 0 && endingCount == 0 {
		issues = append(issues, interactiveIssue(0, "ending", "missing_ending", "全表没有结局段落（ending=true），互动剧必须有至少一个结局"))
	}
	return issues
}

// validateInteractiveEdge 校验单条去向；known 是表中已存在的行号集合。
func validateInteractiveEdge(rowIndex int, edge *InteractiveEdge, known map[int]bool) []InteractiveIssue {
	var issues []InteractiveIssue
	if strings.TrimSpace(edge.Option) == "" {
		issues = append(issues, interactiveIssue(rowIndex, "next.option", "missing_option", "第 %d 行的去向缺少选项文案", rowIndex))
	}
	if edge.Target <= 0 || !known[edge.Target] {
		issues = append(issues, interactiveIssue(rowIndex, "next.target", "target_missing", "第 %d 行的去向目标 %d 不存在", rowIndex, edge.Target))
	}
	return issues
}

// interactiveIssue 统一构造行级问题，避免重复拼装结构。
func interactiveIssue(row int, field string, code string, format string, args ...any) InteractiveIssue {
	return InteractiveIssue{Row: row, Field: field, Code: code, Message: fmt.Sprintf(format, args...)}
}

// normalizeInteractiveRows 清理字段：去首尾空白、角色去重保序。
// 不删除任何行、不截断任何字段，让校验器把超限问题定位到行，而不是静默丢数据。
func normalizeInteractiveRows(rows []InteractiveRow) []InteractiveRow {
	for i := range rows {
		rows[i].Text = strings.TrimSpace(rows[i].Text)
		rows[i].Scene = strings.TrimSpace(rows[i].Scene)
		rows[i].Characters = normalizeInteractiveNames(rows[i].Characters)
		for j := range rows[i].Next {
			rows[i].Next[j].Option = strings.TrimSpace(rows[i].Next[j].Option)
		}
	}
	return rows
}

// normalizeInteractiveNames 去空去重并保持首次出现顺序，名称比较与 CastIndex 一致。
func normalizeInteractiveNames(names []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[NormalizeName(name)] {
			continue
		}
		seen[NormalizeName(name)] = true
		result = append(result, name)
	}
	return result
}

// interactiveCast 汇总全表的角色与场景名称，按首次出现顺序去重，供资产匹配环节按名称检索。
func interactiveCast(rows []InteractiveRow) InteractiveCast {
	characters := make([]string, 0, 16)
	scenes := make([]string, 0, 16)
	characterSeen := map[string]bool{}
	sceneSeen := map[string]bool{}
	for _, row := range rows {
		for _, name := range row.Characters {
			key := NormalizeName(name)
			if !characterSeen[key] {
				characterSeen[key] = true
				characters = append(characters, name)
			}
		}
		key := NormalizeName(row.Scene)
		if row.Scene != "" && !sceneSeen[key] {
			sceneSeen[key] = true
			scenes = append(scenes, row.Scene)
		}
	}
	return InteractiveCast{Characters: characters, Scenes: scenes}
}
