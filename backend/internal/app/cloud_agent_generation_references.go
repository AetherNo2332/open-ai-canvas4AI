package app

import (
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/contract"
)

func validateCloudAgentGenerationReferences(doc map[string]any, nodeID string) error {
	nodes := creationMaps(doc["nodes"])
	index := cloudAgentNodeIndex(nodes, nodeID)
	if index < 0 {
		return BadAuthRequest("生成配置节点不存在")
	}
	raw, err := json.Marshal(cloudAgentNodeMetadata(nodes[index])["generationSpec"])
	if err != nil {
		return err
	}
	spec, err := contract.Decode(raw)
	if err != nil {
		return BadAuthRequest(err.Error())
	}
	for _, binding := range spec.ReferenceBindings {
		if binding.TransientID != "" {
			return BadAuthRequest("编辑生成草稿不能写入临时引用，请使用生成工具解析临时素材")
		}
		if binding.NodeID != "" {
			referenced := cloudAgentNodeIndex(nodes, binding.NodeID)
			if referenced < 0 || binding.NodeID == nodeID {
				return BadAuthRequest("生成引用必须是当前画布的其他节点")
			}
			descriptor, ok := cloudAgentNodeCapabilityForNode(nodes[referenced])
			canReference := ok && descriptor.Connection.CanReference
			if binding.Role == "source-text" && ok {
				canReference = descriptor.Connection.CanSource
			}
			if !canReference || descriptor.InputKind != binding.MediaType {
				return BadAuthRequest("生成参考节点类型不匹配或不可引用")
			}
		}
	}
	return nil
}
