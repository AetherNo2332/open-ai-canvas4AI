package app

import (
	"fmt"
	"strings"
	"unicode"
)

// canvasCommandSpec 是 canvas 工具命令表的唯一声明。Sensitive 管只读拒写与写管道，
// Destructive 管 auto 模式下仍要审批（当前只有 rm）。
type canvasCommandSpec struct {
	Name        string
	Usage       string
	Summary     string
	Sensitive   bool
	Destructive bool
	Action      string
}

var canvasCommandSpecs = []canvasCommandSpec{
	{Name: "pwd", Usage: "pwd", Summary: "打印当前挂载的画布 ID"},
	{Name: "ls", Usage: "ls [-l] [path]", Summary: "列节点目录、字段文件或可创建类型（--types）"},
	{Name: "cat", Usage: "cat [-n] <path>", Summary: "打印节点文档或字段文件原文；-n 带行号"},
	{Name: "grep", Usage: "grep [-n] <path> <pattern...>", Summary: "在字段值行里查找字面量，输出 路径:文本"},
	{Name: "wc", Usage: "wc [-lwc] <path>", Summary: "统计字段值的行数、词数、字符数"},
	{Name: "head", Usage: "head [-n N] <path>", Summary: "打印前 N 行（默认 10）"},
	{Name: "tail", Usage: "tail [-n N] <path>", Summary: "打印后 N 行（默认 10）"},
	{Name: "find", Usage: "find <pattern...>", Summary: "按路径通配查找节点目录与字段文件；* 段内、** 跨段"},
	{Name: "sed", Usage: "sed -i s/<原文>/<替换>/ <字段文件>", Summary: "就地替换字段值里唯一命中的一段文本", Sensitive: true, Action: "就地替换正文"},
	{Name: "new", Usage: "new [--in <submap>] <type> <title...>", Summary: "新建节点，ID 由服务端按高水位发号；可带 heredoc 正文", Sensitive: true, Action: "新建节点"},
	{Name: "dup", Usage: "dup <nodedir> <title...>", Summary: "复制节点；submap 连同子节点复制，内部引用重映射，外部引用不复制", Sensitive: true, Action: "复制节点"},
	{Name: "mv", Usage: "mv <nodedir> <title...>", Summary: "只改节点标题，ID 与路径不变", Sensitive: true, Action: "重命名"},
	{Name: "rm", Usage: "rm <nodedir>", Summary: "删除节点；submap 级联删除子节点，任何权限模式下都需审批", Sensitive: true, Destructive: true, Action: "删除节点"},
	{Name: "run", Usage: "run <nodedir...> [--glob <pattern>] [--mode image|video|audio] [--concurrency N] [--max-retries N] [--canary]", Summary: "按节点 generationSpec 批量触发生成；走批次审批，结果逐条回写各自节点", Sensitive: true, Action: "触发生成"},
	{Name: "edit", Usage: "edit <字段文件> [value...] | edit --glob <pattern> <value...>", Summary: "写字段文件；省略值表示清空", Sensitive: true, Action: "写字段"},
	{Name: "log", Usage: "log [-n N]", Summary: "打印本画布最近的 Agent 写入留痕"},
	{Name: "preset", Usage: "preset list | preset delete <id>", Summary: "列出或删除当前用户的 submap 预制件；save 走 S07 页面入口", Sensitive: false, Action: "管理预制件"},
	{Name: "help", Usage: "help", Summary: "打印命令清单与路径语义"},
}

func canvasCommandSpecFor(name string) (canvasCommandSpec, bool) {
	for _, spec := range canvasCommandSpecs {
		if spec.Name == name {
			return spec, true
		}
	}
	return canvasCommandSpec{}, false
}

func canvasCommandKnown(name string) bool {
	_, ok := canvasCommandSpecFor(name)
	return ok
}

func canvasCommandSensitive(name string) bool {
	spec, ok := canvasCommandSpecFor(name)
	return ok && spec.Sensitive
}

func canvasCommandDestructive(name string) bool {
	spec, ok := canvasCommandSpecFor(name)
	return ok && spec.Destructive
}

func canvasCommandNames(onlySensitive bool) []string {
	names := make([]string, 0, len(canvasCommandSpecs))
	for _, spec := range canvasCommandSpecs {
		if onlySensitive && !spec.Sensitive {
			continue
		}
		names = append(names, spec.Name)
	}
	return names
}

func canvasCommandAction(name string) string {
	if spec, ok := canvasCommandSpecFor(name); ok && spec.Action != "" {
		return spec.Action
	}
	return "执行 " + name + " "
}

func canvasCommandUnknownMessage(name string) string {
	return fmt.Sprintf("不支持的命令 %q；支持的命令：%s。运行 canvas help 看用法与路径语义。参考关系由字段表达：给消费方节点写 referenceNodeIds / referenceTextNodeIds / 首尾帧字段，服务端自动维护连线", name, strings.Join(canvasCommandNames(false), "、"))
}

// canvasCommandStep 是编译后的一条命令。Pipeline 只保存过滤段，第一段是主体。
type canvasCommandStep struct {
	Name     string
	Args     []string
	Number   bool
	Body     string
	Redirect string
	Pipeline []canvasCommandStep
}

func (step canvasCommandStep) sensitive() bool {
	return canvasCommandSensitive(step.Name) || step.Redirect != ""
}

func (step canvasCommandStep) destructive() bool {
	return canvasCommandDestructive(step.Name)
}

// canvasCommandCompile 把一条命令编译成按执行顺序排列的步骤。当前支持 &&、; 和换行；
// || 明确拒绝，避免在原子计划里伪装成支持短路控制流。
func canvasCommandCompile(raw string) ([]canvasCommandStep, error) {
	head, body, heredoc, err := canvasCommandSplitHeredoc(raw)
	if err != nil {
		return nil, canvasCommandError("command", "invalid_value", err.Error())
	}
	segments, err := canvasCommandSplitSegments(head)
	if err != nil {
		return nil, canvasCommandError("command", "invalid_value", err.Error())
	}
	steps := make([]canvasCommandStep, 0, len(segments))
	for index, segment := range segments {
		stepsInSegment, parseErr := canvasCommandParseSegment(segment)
		if parseErr != nil {
			return nil, parseErr
		}
		if index == len(segments)-1 && heredoc {
			attached := false
			for i := len(stepsInSegment) - 1; i >= 0; i-- {
				if stepsInSegment[i].Redirect != "" && stepsInSegment[i].Name == "cat" {
					stepsInSegment[i].Body = body
					attached = true
					break
				}
			}
			if !attached {
				return nil, canvasCommandError("command", "invalid_value", "heredoc 只能接在 cat > <字段文件> 后面")
			}
		}
		steps = append(steps, stepsInSegment...)
	}
	if len(steps) == 0 {
		return nil, canvasCommandError("command", "required", "命令不能为空；运行 canvas help 查看用法")
	}
	return steps, nil
}

// canvasCommandSplitHeredoc 取出命令末尾的 heredoc 正文。heredoc 只允许出现在最后一段，
// 且分隔符必须独占一行结束。
func canvasCommandSplitHeredoc(raw string) (head, body string, ok bool, err error) {
	index := canvasCommandFindHeredoc(raw)
	if index < 0 {
		return raw, "", false, nil
	}
	lineEnd := strings.IndexByte(raw[index:], '\n')
	if lineEnd < 0 {
		return "", "", false, fmt.Errorf("heredoc 缺少正文和结束标记")
	}
	lineEnd += index
	marker := strings.TrimSpace(raw[index:lineEnd])
	marker = strings.TrimPrefix(marker, "<<-")
	marker = strings.TrimPrefix(marker, "<<")
	marker = strings.Trim(strings.TrimSpace(marker), "'\"")
	if marker == "" {
		return "", "", false, fmt.Errorf("heredoc 缺少结束标记")
	}
	rest := raw[lineEnd+1:]
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			body = strings.Join(lines[:i], "\n")
			if i < len(lines)-1 {
				trailing := strings.Join(lines[i+1:], "\n")
				if strings.TrimSpace(trailing) != "" {
					return "", "", false, fmt.Errorf("heredoc 结束后不能再有命令；请拆成多次调用")
				}
			}
			return strings.TrimRight(raw[:index], " 	"), body, true, nil
		}
	}
	return "", "", false, fmt.Errorf("heredoc 结束标记 %q 未找到", marker)
}

func canvasCommandFindHeredoc(raw string) int {
	var quote rune
	for index, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '<':
			if index+1 < len(raw) && raw[index+1] == '<' {
				return index
			}
		}
	}
	return -1
}

func canvasCommandSplitSegments(raw string) ([]string, error) {
	var segments []string
	var current strings.Builder
	var quote rune
	runes := []rune(raw)
	for index := 0; index < len(runes); index++ {
		r := runes[index]
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			current.WriteRune(r)
		case r == '&' && index+1 < len(runes) && runes[index+1] == '&':
			segments = append(segments, current.String())
			current.Reset()
			index++
		case r == '|' && index+1 < len(runes) && runes[index+1] == '|':
			return nil, fmt.Errorf("不支持 || 短路控制；请拆成多次调用，先读取失败原因再决定下一步")
		case r == ';' || r == '\n':
			segments = append(segments, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("引号未闭合")
	}
	segments = append(segments, current.String())
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment) != "" {
			out = append(out, strings.TrimSpace(segment))
		}
	}
	return out, nil
}

func canvasCommandParseSegment(segment string) ([]canvasCommandStep, error) {
	parts, err := canvasCommandSplitPipes(segment)
	if err != nil {
		return nil, canvasCommandError("command", "invalid_value", err.Error())
	}
	steps := make([]canvasCommandStep, 0, len(parts))
	for index, part := range parts {
		tokens, err := canvasCommandTokens(part)
		if err != nil {
			return nil, canvasCommandError("command", "invalid_value", err.Error())
		}
		step, err := canvasCommandParseTokens(tokens)
		if err != nil {
			return nil, err
		}
		if index > 0 && !canvasCommandPipelineFilter(step.Name) {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 不能作为管道过滤器；管道只支持 head/tail/grep/wc", step.Name))
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, canvasCommandError("command", "required", "命令不能为空")
	}
	if len(steps) > 1 {
		if !canvasCommandPipelineSource(steps[0].Name) {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 不能作为管道左侧；左侧只支持 cat/head/tail", steps[0].Name))
		}
		steps[0].Pipeline = steps[1:]
		steps = steps[:1]
	}
	return steps, nil
}

func canvasCommandSplitPipes(segment string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	for _, r := range segment {
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			current.WriteRune(r)
		case r == '|':
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("引号未闭合")
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	return parts, nil
}

func canvasCommandPipelineSource(name string) bool {
	switch name {
	case "cat", "head", "tail":
		return true
	default:
		return false
	}
}

func canvasCommandPipelineFilter(name string) bool {
	switch name {
	case "head", "tail", "grep", "wc":
		return true
	default:
		return false
	}
}

func canvasCommandTokens(command string) ([]string, error) {
	var out []string
	var current strings.Builder
	started := false
	var quote rune
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			started = true
		case unicode.IsSpace(r):
			if started {
				out = append(out, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("引号未闭合")
	}
	if started {
		out = append(out, current.String())
	}
	return out, nil
}

func canvasCommandParseTokens(tokens []string) (canvasCommandStep, error) {
	if len(tokens) == 0 {
		return canvasCommandStep{}, canvasCommandError("command", "required", "命令不能为空")
	}
	step := canvasCommandStep{Name: tokens[0]}
	if !canvasCommandKnown(step.Name) {
		return canvasCommandStep{}, canvasCommandError("command", "unknown_command", canvasCommandUnknownMessage(step.Name))
	}
	rest := tokens[1:]
	// 重定向只允许 cat > <字段文件>：写正文的唯一形态，不接受裸 echo/printf。
	for i := 0; i < len(rest); i++ {
		if rest[i] == ">" {
			if step.Name != "cat" {
				return canvasCommandStep{}, canvasCommandError("command", "unsupported", "只支持 cat > <字段文件> 写正文；其它命令没有重定向")
			}
			if i == 0 || i+1 >= len(rest) {
				return canvasCommandStep{}, canvasCommandError("command", "invalid_value", "cat > 缺少字段文件路径")
			}
			step.Redirect = rest[i+1]
			rest = append(append([]string{}, rest[:i]...), rest[i+2:]...)
			break
		}
	}
	switch step.Name {
	case "cat", "grep":
		rest, step.Number = canvasCommandStripNumberFlag(rest)
	case "wc":
		rest, _ = canvasCommandStripWordCountFlags(rest)
	case "ls":
		rest, step.Number = canvasCommandStripNumberFlag(rest)
	}
	step.Args = rest
	return step, nil
}

func canvasCommandStripNumberFlag(args []string) ([]string, bool) {
	numbered := false
	out := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "-n":
			numbered = true
		case "-l":
			out = append(out, arg)
		default:
			out = append(out, arg)
		}
	}
	return out, numbered
}

func canvasCommandStripWordCountFlags(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "-l", "-w", "-c", "-lwc", "-lc", "-lw":
			continue
		default:
			out = append(out, arg)
		}
	}
	return out, true
}

func canvasCommandError(field, issue, message string) error {
	return cloudAgentFieldError(field, issue, message)
}
