package connection

import "fmt"

// InputKind returns the media kind a node contributes when it is used as a
// connection source. Character is a canvas variant of a text node.
func InputKind(node Node) string {
	if node.WorkflowKind == "character" {
		return "character"
	}
	return Builtins[node.Type].InputKind
}

// ValidateManualInputs applies graph-admission rules that are stricter than
// the generic editor connection matrix. Endpoint existence and graph-port
// permissions are validated by the caller before this function.
func ValidateManualInputs(nodes []Node, edges []Edge, candidate Edge) error {
	lookup := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		lookup[node.ID] = node
	}
	from, fromOK := lookup[candidate.From]
	to, toOK := lookup[candidate.To]
	if !fromOK || !toOK {
		return fmt.Errorf("连线端点不存在")
	}
	fromKind := InputKind(from)
	if err := validateManualTarget(from, to, fromKind); err != nil {
		return err
	}
	return validateManualMode(lookup, fromKind, to, edges, candidate)
}

func validateManualTarget(from, to Node, fromKind string) error {
	if fromKind == "character" && to.Type == "text" && to.Mode == "" {
		return fmt.Errorf("角色卡只能连接生成节点，不能连接普通文本节点")
	}
	if to.Type == "media-conversion" && from.Type != "image" && from.Type != "video" {
		return fmt.Errorf("转换节点只接受图片或视频节点")
	}
	if to.Type == "batch-table" && fromKind != "image" {
		return fmt.Errorf("批量创作表只接受图片输入")
	}
	return nil
}

func validateManualMode(lookup map[string]Node, fromKind string, to Node, edges []Edge, candidate Edge) error {
	switch Mode(to) {
	case "image":
		if fromKind == "video" || fromKind == "audio" {
			return fmt.Errorf("图片生成节点不能连接参考视频或音频")
		}
	case "text":
		if fromKind == "audio" {
			return fmt.Errorf("文本生成节点不能连接参考音频")
		}
	case "audio":
		if fromKind != "text" && fromKind != "character" {
			return fmt.Errorf("音频生成只接受文本或角色卡")
		}
		if fromKind == "character" && characterInputCount(lookup, edges, candidate) > 1 {
			return fmt.Errorf("角色配音一次只能连接一个角色卡")
		}
	}
	return nil
}

// GraphInputLimit is the hard graph-input cap that applies independently of
// the selected generation model's reference limits.
func GraphInputLimit(nodeType string) int {
	if nodeType == "media-conversion" {
		return 1
	}
	return 0
}

func characterInputCount(lookup map[string]Node, edges []Edge, candidate Edge) int {
	ids := map[string]bool{candidate.From: true}
	for _, edge := range edges {
		if edge.To == candidate.To {
			ids[edge.From] = true
		}
	}
	count := 0
	for id := range ids {
		if node, ok := lookup[id]; ok && InputKind(node) == "character" {
			count++
		}
	}
	return count
}
