package references

import (
	"regexp"
	"strings"

	"infinite-canvas/backend/internal/canvas/contract"
)

var promptHandlePattern = regexp.MustCompile(`\{\{ref:([^{}\r\n]+)\}\}`)

// PromptHandle reuses the declared binding identity, never its changing order.
func PromptHandle(bindingID string) string { return "{{ref:" + bindingID + "}}" }

// ResolvePromptHandles is called only after the final provider slots are known.
// Text bindings have no media slot and cannot be mistaken for an image index.
func ResolvePromptHandles(prompt string, bindings []contract.ReferenceBinding, nodeSlots map[string]string) (string, error) {
	available := map[string]string{}
	for _, b := range bindings {
		if b.MediaType != "text" {
			available[b.ID] = nodeSlots[b.NodeID]
		}
	}
	var issue error
	resolved := promptHandlePattern.ReplaceAllStringFunc(prompt, func(token string) string {
		id := promptHandlePattern.FindStringSubmatch(token)[1]
		if slot := available[id]; slot != "" {
			return slot
		}
		issue = fail("prompt_reference_missing", "prompt", "提示词引用的资产不在当前有效输入中："+id+"；请重新读取该行资产，不能用其他素材代替")
		return token
	})
	if issue != nil {
		return "", issue
	}
	if strings.Contains(resolved, "{{ref:") {
		return "", fail("invalid_prompt_reference", "prompt", "资产句柄格式无效，请复制读取回执中的 promptHandle")
	}
	return resolved, nil
}
