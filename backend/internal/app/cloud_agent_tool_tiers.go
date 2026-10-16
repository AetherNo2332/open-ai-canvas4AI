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
	"canvas_read_content":      true,
	"canvas_search_nodes":      true,
}

type cloudAgentCanvasInventory struct {
	NodeTypes    []string
	HasCharacter bool
	TotalNodes   int
}

type cloudAgentToolIntent struct {
	Storyboard  bool
	BatchTable  bool
	Drawing     bool
	Assets      bool
	Character   bool
	Arrange     bool
	LayerSplit  bool
	OCR         bool
	Annotation  bool
	LongText    bool
	SearchNodes bool
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
		Storyboard:  cloudAgentPromptHasAny(prompt, "分镜", "逐镜", "storyboard"),
		BatchTable:  cloudAgentPromptHasAny(prompt, "批次", "批量创作"),
		Drawing:     cloudAgentPromptHasAny(prompt, "绘图", "笔迹", "tldraw", "excalidraw"),
		Assets:      cloudAgentPromptHasAny(prompt, "素材库", "替换素材", "绑定素材"),
		Character:   cloudAgentPromptHasAny(prompt, "角色卡"),
		Arrange:     cloudAgentPromptHasAny(prompt, "整理", "排版", "对齐"),
		LayerSplit:  cloudAgentPromptHasAny(prompt, "拆图层"),
		OCR:         cloudAgentPromptHasAny(prompt, "认字", "ocr"),
		Annotation:  cloudAgentPromptHasAny(prompt, "标注"),
		LongText:    cloudAgentPromptHasAny(prompt, "长文", "全文"),
		SearchNodes: cloudAgentPromptHasAny(prompt, "搜索画布"),
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
		enable("canvas_read_storyboard", "canvas_create_storyboard", "canvas_edit_storyboard")
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
	if intent.LongText {
		enable("canvas_read_content")
	}
	if intent.SearchNodes || inventory.TotalNodes > cloudAgentCanvasSummaryMaxNodes {
		enable("canvas_search_nodes")
	}
	return unlocked
}

func cloudAgentAdvertisedToolNames(req CloudAgentRequest, inventory cloudAgentCanvasInventory, prompt string) []string {
	unlocked := cloudAgentUnlockedL1Tools(inventory, cloudAgentPromptIntentFlags(prompt))
	eligible := cloudAgentTools(req)
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
