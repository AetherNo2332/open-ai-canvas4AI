package app

import "yingce/backend/internal/canvas/capability"

// Canvas capabilities are registered once at the canvas domain boundary. Agent
// tools, creation, media and state projection all consume this registry; there
// is no Agent-only node allow-list to keep in sync.
type cloudAgentNodeCapability = capability.Descriptor

type CloudAgentCapabilitySet struct {
	Version string
	Hash    string
	Nodes   []string
}

const cloudAgentCapabilitySetVersion = capability.SetVersion

var canvasCapabilityRegistry = capability.BuiltinRegistry()

func cloudAgentNodeCapabilityForType(nodeType string) (cloudAgentNodeCapability, bool) {
	return canvasCapabilityRegistry.Resolve(nodeType)
}

// cloudAgentNodeCapabilityForNode 按画布节点本身解析能力：角色卡等 workflowKind 变体
// 优先于底层类型。凡是手里有真实节点（读取、连线、引用、更新）的调用方都应使用它；
// 只有 add_node 这类按类型名创建的入口才使用 cloudAgentNodeCapabilityForType。
func cloudAgentNodeCapabilityForNode(node map[string]any) (cloudAgentNodeCapability, bool) {
	meta, _ := node["metadata"].(map[string]any)
	return canvasCapabilityRegistry.ResolveNode(stringValue(node["type"]), stringValue(meta["workflowKind"]))
}

func cloudAgentNodeTypeNames() []string { return canvasCapabilityRegistry.Types() }

func cloudAgentEditableNodeProjection(node map[string]any, descriptor cloudAgentNodeCapability, textLimit int) map[string]any {
	values, truncated := descriptor.EditableValues(node, textLimit)
	result := map[string]any{"fields": values}
	if len(truncated) > 0 {
		result["truncatedFields"] = truncated
	}
	return result
}

func cloudAgentGenerationModeNames() []string {
	modes := make([]string, 0)
	for _, mode := range canvasCapabilityRegistry.GenerationModeNames() {
		if cloudAgentGenerationModeSupported(mode) {
			modes = append(modes, mode)
		}
	}
	return modes
}

func cloudAgentGenerationModeSupported(mode string) bool {
	_, implemented := cloudAgentGenerationAdapters[mode]
	return implemented && canvasCapabilityRegistry.SupportsGenerationMode(mode)
}

func cloudAgentNodeCapabilityForGenerationMode(mode string) (cloudAgentNodeCapability, bool) {
	return canvasCapabilityRegistry.ResolveGenerationMode(mode)
}

func cloudAgentCapabilitySetHash() string { return canvasCapabilityRegistry.Hash() }

func CloudAgentCapabilitySetInfo() CloudAgentCapabilitySet {
	return CloudAgentCapabilitySet{Version: cloudAgentCapabilitySetVersion, Hash: cloudAgentCapabilitySetHash(), Nodes: cloudAgentNodeTypeNames()}
}

// cloudAgentAddNodeTypeNames 返回 add_node 可创建的节点类型；分镜表工具关闭时
// 剔除 script（分镜节点），Agent 以文本/Markdown 节点承载分镜内容，不能绕过
// 开关用画布写入变相重建分镜。
func cloudAgentAddNodeTypeNames(storyboardDisabled bool) []string {
	names := cloudAgentNodeTypeNames()
	if !storyboardDisabled {
		return names
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if name == "script" {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

// validateCloudAgentStoryboardCreateGate 分镜功能关闭时的创建闸：禁止用
// canvas_apply_ops 绕过开关创建分镜（script）节点，包括复制已有分镜节点。
// 已有 script 节点的正文更新不受影响。Agent 端被披露+执行双闸拦截；UI 侧
// 用户手工创建仍走前端能力不受此约束。
func validateCloudAgentStoryboardCreateGate(ops []agentCanvasOp, storyboardDisabled bool, doc map[string]any) error {
	if !storyboardDisabled {
		return nil
	}
	nodes := creationMaps(doc["nodes"])
	for _, op := range ops {
		switch op.Type {
		case "add_node":
			if op.NodeType == "script" {
				return cloudAgentFieldError("ops", "storyboard_disabled", "分镜功能已关闭：不能创建分镜（脚本）节点；请用文本节点或 Markdown 节点承载分镜内容，或请用户在 Agent 设置重新开启分镜")
			}
		case "duplicate_node":
			index := cloudAgentNodeIndex(nodes, op.SourceNodeID)
			if index < 0 || stringValue(nodes[index]["type"]) == "script" {
				return cloudAgentFieldError("ops.sourceNodeId", "storyboard_disabled", "分镜功能已关闭：不能复制分镜（脚本）节点；分镜内容请落在新文本节点或 Markdown 节点上")
			}
		}
	}
	return nil
}
