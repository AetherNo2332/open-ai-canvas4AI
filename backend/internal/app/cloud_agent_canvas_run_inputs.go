package app

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/canvas/contract"
)

var (
	canvasTextMention      = regexp.MustCompile(`@文本(\d+)`)
	canvasCharacterMention = regexp.MustCompile(`@角色(\d+)`)
	canvasNodeMention      = regexp.MustCompile(`@\[node:([^\]]+)\]`)
)

// 字段引用是运行时真源；旧 connections 只作为兼容补充。顺序与编辑器输入槽一致：
// referenceNodeIds → 首尾帧 → text 引用。
func cloudAgentCanvasMediaInputIDs(node map[string]any) []string {
	meta, _ := node["metadata"].(map[string]any)
	out := make([]string, 0, 8)
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range cloudAgentStrings(meta["referenceNodeIds"]) {
		add(id)
	}
	add(stringValue(meta["videoStartFrameNodeId"]))
	add(stringValue(meta["videoEndFrameNodeId"]))
	for _, binding := range cloudAgentCanvasSpecBindings(meta) {
		switch binding.Role {
		case "reference", "first-frame", "last-frame", "mask":
			add(binding.NodeID)
		}
	}
	return out
}

func cloudAgentCanvasTextInputIDs(node map[string]any) []string {
	meta, _ := node["metadata"].(map[string]any)
	out := make([]string, 0, 4)
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range cloudAgentStrings(meta["referenceTextNodeIds"]) {
		add(id)
	}
	for _, binding := range cloudAgentCanvasSpecBindings(meta) {
		if binding.Role == "source-text" {
			add(binding.NodeID)
		}
	}
	return out
}

func cloudAgentCanvasSpecBindings(meta map[string]any) []contract.ReferenceBinding {
	raw, exists := meta["generationSpec"]
	if !exists || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	spec, err := contract.Decode(encoded)
	if err != nil {
		return nil
	}
	return spec.ReferenceBindings
}

// cloudAgentCanvasComposePrompt 复刻编辑器的生成输入编译：
// 节点提及转成槽位标签，文本块按引用顺序追加到末尾。图片/视频/音频的 @槽位
// 仍由既有 cloudAgentMediaReferencePrompt 校验，避免出现第二份媒体引用规则。
func cloudAgentCanvasComposePrompt(prompt string, node map[string]any, nodes map[string]map[string]any, mediaIDs []string) (string, error) {
	textIDs := cloudAgentCanvasTextInputIDs(node)
	textIndex := map[string]int{}
	for index, id := range textIDs {
		textIndex[id] = index + 1
	}
	mediaTokens := map[string]string{}
	counts := map[string]int{}
	characterTokens := make([]string, 0, 2)
	for _, id := range mediaIDs {
		source := nodes[id]
		if source == nil {
			continue
		}
		descriptor, known := cloudAgentNodeCapabilityForNode(source)
		kind := descriptor.InputKind
		if !known || kind == "" {
			kind = stringValue(source["type"])
		}
		if kind == "character" {
			kind = "image"
		}
		label := map[string]string{"image": "图片", "video": "视频", "audio": "音频"}[kind]
		if label == "" {
			continue
		}
		counts[kind]++
		token := fmt.Sprintf("@%s%d", label, counts[kind])
		mediaTokens[id] = token
		if kind == "image" && cloudAgentCharacterNode(source) {
			characterTokens = append(characterTokens, token)
		}
	}

	usedText := map[int]bool{}
	var composeErr error
	prompt = canvasNodeMention.ReplaceAllStringFunc(prompt, func(token string) string {
		if composeErr != nil {
			return token
		}
		match := canvasNodeMention.FindStringSubmatch(token)
		id := match[1]
		if index, ok := textIndex[id]; ok {
			usedText[index] = true
			return fmt.Sprintf("【文本%d】", index)
		}
		if value, ok := mediaTokens[id]; ok {
			return value
		}
		composeErr = fmt.Errorf("提示词引用的节点 %s 不在本节点的参考字段里", id)
		return token
	})
	if composeErr != nil {
		return "", composeErr
	}
	prompt = canvasTextMention.ReplaceAllStringFunc(prompt, func(token string) string {
		if composeErr != nil {
			return token
		}
		index, _ := strconv.Atoi(canvasTextMention.FindStringSubmatch(token)[1])
		if index < 1 || index > len(textIDs) {
			composeErr = fmt.Errorf("提示词引用了 %s，但节点只有 %d 条文本引用；请按 referenceTextNodeIds 顺序修正", token, len(textIDs))
			return token
		}
		usedText[index] = true
		return fmt.Sprintf("【文本%d】", index)
	})
	if composeErr != nil {
		return "", composeErr
	}
	prompt = canvasCharacterMention.ReplaceAllStringFunc(prompt, func(token string) string {
		if composeErr != nil {
			return token
		}
		index, _ := strconv.Atoi(canvasCharacterMention.FindStringSubmatch(token)[1])
		if index < 1 || index > len(characterTokens) {
			composeErr = fmt.Errorf("提示词引用了 %s，但节点只有 %d 个角色引用", token, len(characterTokens))
			return token
		}
		return characterTokens[index-1]
	})
	if composeErr != nil {
		return "", composeErr
	}
	if len(usedText) == 0 {
		return prompt, nil
	}
	indexes := make([]int, 0, len(usedText))
	for index := range usedText {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var out strings.Builder
	out.WriteString(strings.TrimRight(prompt, " \t\r\n"))
	for _, index := range indexes {
		content := cloudAgentCanvasTextContent(nodes[textIDs[index-1]])
		out.WriteString(fmt.Sprintf("\n\n【文本%d】\n%s", index, content))
	}
	result := out.String()
	if utf8.RuneCountInString(result) > 16000 {
		return "", fmt.Errorf("拼接文本引用后的提示词共 %d 字，超过 16000 字上限；请精简文本节点", utf8.RuneCountInString(result))
	}
	return result, nil
}

func cloudAgentCanvasTextContent(node map[string]any) string {
	if node == nil {
		return ""
	}
	meta, _ := node["metadata"].(map[string]any)
	switch stringValue(node["type"]) {
	case "text", "markdown":
		if text := strings.TrimSpace(stringValue(meta["content"])); text != "" {
			return text
		}
	case "script":
		if text := strings.TrimSpace(stringValue(meta["composerContent"])); text != "" {
			return text
		}
	}
	for _, key := range []string{"content", "composerContent", "prompt"} {
		if text := strings.TrimSpace(stringValue(meta[key])); text != "" {
			return text
		}
	}
	return ""
}
