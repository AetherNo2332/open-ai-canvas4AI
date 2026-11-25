package canvas

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/kernel"
)

// CanvasClientCapabilityReferenceFields marks a client that understands
// referenceFieldsVersion documents and writes relations through node fields.
const CanvasClientCapabilityReferenceFields = "canvas-reference-fields/v1"

func validateCanvasClientCapabilities(values []string) error {
	if len(values) > 16 {
		return kernel.BadAuthRequest("画布客户端能力数量超出限制")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 80 {
			return kernel.BadAuthRequest("画布客户端能力标识无效")
		}
		if _, duplicate := seen[value]; duplicate {
			return kernel.BadAuthRequest("画布客户端能力标识重复")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func canvasClientSupportsReferenceFields(req CanvasOperationsRequest) bool {
	for _, capability := range req.ClientCapabilities {
		if strings.TrimSpace(capability) == CanvasClientCapabilityReferenceFields {
			return true
		}
	}
	return false
}

func requireCanvasClientCapability(req CanvasOperationsRequest) error {
	if canvasClientSupportsReferenceFields(req) {
		return nil
	}
	return canvasMutationError(
		"canvas_client_capability_required",
		"画布已使用字段引用格式，当前客户端过旧；请刷新页面后再保存",
	)
}

func canvasDocumentUsesFieldReferences(payload map[string]json.RawMessage) bool {
	raw := payload["referenceFieldsVersion"]
	if len(raw) == 0 {
		return false
	}
	var version float64
	return json.Unmarshal(raw, &version) == nil && version == float64(references.Version)
}

func canvasDocumentMapUsesFieldReferences(document map[string]any) bool {
	return references.Enabled(document)
}

func rejectLegacyCanvasConnectionOperations(operations []CanvasOperation) error {
	for _, operation := range operations {
		switch operation.Type {
		case "create_connection", "update_connection", "remove_connection":
			return canvasMutationError(
				"canvas_connection_operations_retired",
				"字段引用格式不接受连线写操作；请更新节点引用字段并刷新客户端",
			)
		}
	}
	return nil
}

func hasCanvasConnectionOperations(operations []CanvasOperation) bool {
	for _, operation := range operations {
		switch operation.Type {
		case "create_connection", "update_connection", "remove_connection":
			return true
		}
	}
	return false
}

func diffCanvasConnectionsIfLegacy(before, after map[string]any) []CanvasOperation {
	if canvasDocumentMapUsesFieldReferences(before) || canvasDocumentMapUsesFieldReferences(after) {
		return nil
	}
	return diffCanvasConnections(before, after)
}

func diffCanvasConnections(before, after map[string]any) []CanvasOperation {
	operations := make([]CanvasOperation, 0)
	beforeEdges := indexedCanvasObjects(before["connections"])
	afterEdges := indexedCanvasObjects(after["connections"])
	for _, edge := range orderedCanvasObjects(after["connections"]) {
		id := canvasString(edge["id"])
		if previous, exists := beforeEdges[id]; !exists {
			operations = append(operations, CanvasOperation{Type: "create_connection", Connection: jsonRawValue(edge)})
		} else if !jsonEqual(previous, edge) {
			operations = append(operations, CanvasOperation{Type: "update_connection", ID: id, Connection: jsonRawValue(edge), Expected: jsonRawValue(previous)})
		}
	}
	for _, edge := range orderedCanvasObjects(before["connections"]) {
		id := canvasString(edge["id"])
		if _, exists := afterEdges[id]; !exists {
			operations = append(operations, CanvasOperation{Type: "remove_connection", ID: id, Expected: jsonRawValue(edge)})
		}
	}
	return operations
}

func validateCanvasDocumentOperationFormat(payload map[string]json.RawMessage, operations []CanvasOperation, req CanvasOperationsRequest) error {
	if !canvasDocumentUsesFieldReferences(payload) {
		return nil
	}
	if err := requireCanvasClientCapability(req); err != nil {
		return err
	}
	return rejectLegacyCanvasConnectionOperations(operations)
}

func validateCanvasCreateFormatField(field string, value json.RawMessage, req CanvasOperationsRequest) error {
	if field != "referenceFieldsVersion" {
		return nil
	}
	if err := requireCanvasClientCapability(req); err != nil {
		return err
	}
	var version int
	if err := json.Unmarshal(value, &version); err != nil || version != references.Version {
		return kernel.BadAuthRequest("不支持的画布引用格式版本")
	}
	return nil
}
