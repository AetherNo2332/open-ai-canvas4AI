package production

import (
	"fmt"
	"strings"
)

const jsonOnlyInteractive = "只输出一个 JSON 对象，不要 Markdown 代码块，不要解释。"

// InteractiveSplitSystemPrompt 约束拆分 Agent 的角色与输出纪律。
func InteractiveSplitSystemPrompt() string {
	return "你是互动剧拆分师。你把剧本文本拆成一行一个可制作剧情段落的表格，只拆不改：" +
		"不增删情节、不改写台词、不调整原文顺序。" + jsonOnlyInteractive
}

// InteractiveSplitPrompt 生成拆分提示词。title 是作品名，requirements 是编剧的改编要求
// （可以为空），script 是待拆分的剧本原文。
func InteractiveSplitPrompt(title string, requirements string, script string) string {
	title = strings.TrimSpace(title)
	requirements = strings.TrimSpace(requirements)
	if requirements == "" {
		requirements = "无特殊改编要求，按原文结构拆分"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "作品：%s\n改编要求：%s\n\n", firstNonEmpty(title, "（未命名作品）"), requirements)
	b.WriteString("【固定表头】每行一个可制作的剧情段落，字段定义如下：\n")
	b.WriteString("- index：行号，从 1 开始连续编号，全表唯一；行号 1 是入口节点。\n")
	b.WriteString("- text：正文，本段落可拍摄的剧情内容，保留原文台词与动作描写顺序。\n")
	b.WriteString("- characters：本段落出镜角色名列表，使用原文称呼，没有出镜角色则空数组。\n")
	b.WriteString("- scene：本段落发生的场景名，使用原文称呼。\n")
	b.WriteString("- next：去向列表，每个去向是 {\"target\":目标行号,\"option\":\"选项文案\",\"suggested\":布尔}；结局段落 next 为空数组。\n")
	b.WriteString("- ending：布尔值，标记本段落是结局（故事终点）；结局段落 next 必须为空数组。\n")
	b.WriteString("- suggested：布尔值，标记本段落是拆分新增的建议段落，供编剧确认；原文已有的段落为 false。\n\n")
	b.WriteString("【拆分规则】\n")
	b.WriteString("1. 一行一个可制作的剧情段落：场景、出场角色或拍摄意图发生变化时另起一行。\n")
	b.WriteString("2. 原文已有的分支必须保留：去向的 target 指向原文对应段落的行号，option 逐字保留原文的选项文案，suggested 为 false。\n")
	b.WriteString("3. 原文是单线故事时，可以在自然停顿处新增分支建议：新增段落或新增去向的 suggested 标记为 true，并写好建议的选项文案；不得改动原文已有段落的内容与顺序。\n")
	b.WriteString("4. 结局段落（故事终点）ending=true 且 next=[]；全剧至少有一个结局。\n")
	b.WriteString("5. 非结局段落至少有一个去向；去向的 target 必须是本表中已存在的行号。\n")
	b.WriteString("6. 每个去向必须带 option 文案；线性续接没有选择时也写 option=\"继续\"。\n")
	b.WriteString("7. characters 与 scene 用原文称呼，不要自造名称；同一段落内角色名去重。\n")
	b.WriteString("8. text 保留原文台词与动作描写的顺序，可以合并同一段落内连续的动作描写，但不得改写、缩写或增补台词。\n\n")
	b.WriteString("输出（顶层 title 为作品标题）：\n")
	b.WriteString(`{"title":"作品标题","rows":[{"index":1,"text":"正文","characters":["角色名"],"scene":"场景名","next":[{"target":2,"option":"选项文案","suggested":false}],"ending":false,"suggested":false}]}` + "\n\n")
	b.WriteString("【剧本原文】\n")
	b.WriteString(script)
	return b.String()
}
