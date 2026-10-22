package app

import (
	"encoding/json"
	"strings"
)

// L1 是 schema 重或依赖特定节点/意图的具体工具。过滤器只对合格目录做减法：
// 未解锁的 L1 不下发；其余合格工具（控制、导演台、子代理、技能、L0 画布/生成）保持原样。
var cloudAgentTierL1Tools = map[string]bool{
	"canvas_read_storyboard":   true,
	"canvas_create_storyboard": true,
	"canvas_edit_storyboard":   true,
	"canvas_read_batch_table":  true,
	"canvas_edit_batch_table":  true,
	"canvas_read_drawing":      true,
	"canvas_edit_drawing":      true,
	"canvas_list_assets":       true,
	"canvas_bind_asset":        true,
	"canvas_create_character":  true,
	"canvas_arrange_nodes":     true,
	"image_layer_split":        true,
	"image_text_detect":        true,
	"image_annotation_render":  true,
}

type cloudAgentCanvasInventory struct {
	NodeTypes    []string
	HasCharacter bool
	TotalNodes   int
}

type cloudAgentToolIntent struct {
	Storyboard bool
	BatchTable bool
	Drawing    bool
	Assets     bool
	Character  bool
	Arrange    bool
	LayerSplit bool
	OCR        bool
	Annotation bool
}

func cloudAgentScanCanvasInventory(payloadJSON string) cloudAgentCanvasInventory {
	var payload struct {
		Nodes []struct {
			Type     string `json:"type"`
			Metadata struct {
				WorkflowKind string `json:"workflowKind"`
			} `json:"metadata"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return cloudAgentCanvasInventory{}
	}
	seen := map[string]bool{}
	types := make([]string, 0, 8)
	hasCharacter := false
	for _, node := range payload.Nodes {
		if node.Type != "" && !seen[node.Type] {
			seen[node.Type] = true
			types = append(types, node.Type)
		}
		if node.Type == "text" && node.Metadata.WorkflowKind == "character" {
			hasCharacter = true
		}
	}
	return cloudAgentCanvasInventory{NodeTypes: types, HasCharacter: hasCharacter, TotalNodes: len(payload.Nodes)}
}

func cloudAgentInventoryHasType(inventory cloudAgentCanvasInventory, nodeType string) bool {
	for _, existing := range inventory.NodeTypes {
		if existing == nodeType {
			return true
		}
	}
	return false
}

func cloudAgentPromptHasAny(prompt string, keywords ...string) bool {
	lower := strings.ToLower(prompt)
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func cloudAgentPromptIntentFlags(prompt string) cloudAgentToolIntent {
	return cloudAgentToolIntent{
		Storyboard: cloudAgentPromptHasAny(prompt, "分镜", "逐镜", "storyboard"),
		BatchTable: cloudAgentPromptHasAny(prompt, "批次", "批量创作"),
		Drawing:    cloudAgentPromptHasAny(prompt, "绘图", "笔迹", "tldraw", "excalidraw"),
		Assets:     cloudAgentPromptHasAny(prompt, "素材库", "替换素材", "绑定素材"),
		Character:  cloudAgentPromptHasAny(prompt, "角色卡"),
		Arrange:    cloudAgentPromptHasAny(prompt, "整理", "排版", "对齐"),
		LayerSplit: cloudAgentPromptHasAny(prompt, "拆图层"),
		OCR:        cloudAgentPromptHasAny(prompt, "认字", "ocr"),
		Annotation: cloudAgentPromptHasAny(prompt, "标注"),
	}
}

func cloudAgentUnlockedL1Tools(inventory cloudAgentCanvasInventory, intent cloudAgentToolIntent) map[string]bool {
	unlocked := map[string]bool{}
	enable := func(names ...string) {
		for _, name := range names {
			unlocked[name] = true
		}
	}
	if cloudAgentInventoryHasType(inventory, "script") || intent.Storyboard {
		enable("canvas_read_storyboard", "canvas_create_storyboard", "canvas_edit_storyboard", "canvas_create_character", "canvas_list_assets", "canvas_bind_asset")
	}
	if cloudAgentInventoryHasType(inventory, "batch-table") || intent.BatchTable {
		enable("canvas_read_batch_table", "canvas_edit_batch_table")
	}
	if cloudAgentInventoryHasType(inventory, "drawing") || intent.Drawing {
		enable("canvas_read_drawing", "canvas_edit_drawing")
	}
	if intent.Assets {
		enable("canvas_list_assets", "canvas_bind_asset")
	}
	if inventory.HasCharacter || intent.Character {
		enable("canvas_create_character")
	}
	if intent.Arrange {
		enable("canvas_arrange_nodes")
	}
	if intent.LayerSplit {
		enable("image_layer_split")
	}
	if intent.OCR {
		enable("image_text_detect")
	}
	if intent.Annotation {
		enable("image_annotation_render")
	}
	return unlocked
}

// A model boundary may add eligible tools, but an in-flight model or tool batch
// retains its admitted loadout. Pending/subagent text is not a user instruction.
func cloudAgentNextAdvertisedToolNames(state *cloudAgentRuntime, inventory cloudAgentCanvasInventory) []string {
	if len(state.AdvertisedToolNames) == 0 {
		return cloudAgentToolNames(state.Canonical.Tools)
	}
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) {
		return append([]string(nil), state.AdvertisedToolNames...)
	}
	prompt := state.Request.Prompt
	for _, message := range state.Canonical.Messages {
		source := stringField(message, cloudAgentContextSourceKey)
		if stringField(message, "role") == "user" && source == "user_interjection" {
			prompt += "\n" + stringField(message, "content")
		}
	}
	selected := map[string]bool{}
	for _, name := range append(append(append([]string(nil), state.AdvertisedToolNames...), state.DeliveredIntentToolNames...), cloudAgentSelectAdvertisedTools(state.Canonical.Tools, inventory, prompt)...) {
		selected[name] = true
	}
	names := []string{}
	eligible := map[string]bool{}
	for _, name := range cloudAgentToolNames(state.Canonical.Tools) {
		eligible[name] = true
	}
	for _, name := range append(append([]string(nil), state.AdvertisedToolNames...), cloudAgentToolNames(state.Canonical.Tools)...) {
		if eligible[name] && selected[name] && (name == "read" && state.SkillRuntimeMode == cloudAgentSkillRuntimeNative || cloudAgentToolAllowed(state.Request, name)) {
			names = append(names, name)
			delete(selected, name)
		}
	}
	return names
}

func cloudAgentRecordDeliveredToolIntent(state *cloudAgentRuntime, item cloudAgentInterjection) {
	if item.Source == "subagent" {
		return
	}
	unlocked := cloudAgentUnlockedL1Tools(cloudAgentCanvasInventory{}, cloudAgentPromptIntentFlags(item.Text))
	for _, name := range cloudAgentToolNames(state.Canonical.Tools) {
		if unlocked[name] && cloudAgentTierL1Tools[name] && !cloudAgentContainsString(state.DeliveredIntentToolNames, name) {
			state.DeliveredIntentToolNames = append(state.DeliveredIntentToolNames, name)
		}
	}
}

func cloudAgentAdvertisedToolNames(req CloudAgentRequest, inventory cloudAgentCanvasInventory, prompt string) []string {
	return cloudAgentSelectAdvertisedTools(cloudAgentTools(req), inventory, prompt)
}

func cloudAgentSelectAdvertisedTools(eligible []map[string]any, inventory cloudAgentCanvasInventory, prompt string) []string {
	unlocked := cloudAgentUnlockedL1Tools(inventory, cloudAgentPromptIntentFlags(prompt))
	names := make([]string, 0, len(eligible))
	for _, tool := range eligible {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		if name == "" {
			continue
		}
		if cloudAgentTierL1Tools[name] && !unlocked[name] {
			continue
		}
		names = append(names, name)
	}
	return names
}

func cloudAgentToolsNamed(all []map[string]any, names []string) []map[string]any {
	set := map[string]bool{}
	for _, name := range names {
		if name != "" {
			set[name] = true
		}
	}
	out := make([]map[string]any, 0, len(set))
	for _, tool := range all {
		function, _ := tool["function"].(map[string]any)
		if set[stringField(function, "name")] {
			out = append(out, tool)
		}
	}
	return out
}

func cloudAgentToolAdvertisedForRun(state *cloudAgentRuntime, name string) bool {
	if state == nil || name == "" {
		return false
	}
	if len(state.AdvertisedToolNames) == 0 {
		return true
	}
	for _, advertised := range state.AdvertisedToolNames {
		if advertised == name {
			return true
		}
	}
	return false
}
