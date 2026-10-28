package references

import (
	"encoding/json"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/contract"
)

func referenceMediaMatches(d capability.Descriptor, b contract.ReferenceBinding) bool {
	return d.InputKind == b.MediaType || d.InputKind == "character" && (b.MediaType == "image" || b.MediaType == "audio" || b.MediaType == "text")
}

func validateReferenceCapability(d capability.Descriptor, b contract.ReferenceBinding) error {
	allowed := d.Connection.CanReference
	if b.Role == "source-text" {
		allowed = d.Connection.CanSource
	}
	if !allowed || !referenceMediaMatches(d, b) {
		return fail("invalid_reference", "generationSpec.referenceBindings", "参考内容与节点类型不符或不支持此用法")
	}
	return nil
}

// ValidateGenerationReferences shares the editor and Agent draft policy.
// Character is a business kind; its image/audio/text representations stay media kinds.
func ValidateGenerationReferences(doc map[string]any, nodeID string, registry *capability.Registry) error {
	node := index(doc)[nodeID]
	if node == nil {
		return fail("reference_missing", "nodeId", "生成配置节点不存在")
	}
	raw, err := json.Marshal(meta(node)["generationSpec"])
	if err != nil {
		return err
	}
	spec, err := contract.Decode(raw)
	if err != nil {
		return fail("invalid_reference", "generationSpec", err.Error())
	}
	for _, b := range spec.ReferenceBindings {
		if b.TransientID != "" {
			return fail("invalid_reference", "generationSpec.referenceBindings", "临时素材尚未解析，请重新选择参考内容后保存")
		}
	}
	_, err = Resolve(doc, Scope{Kind: "generation", NodeID: nodeID}, registry)
	return err
}
