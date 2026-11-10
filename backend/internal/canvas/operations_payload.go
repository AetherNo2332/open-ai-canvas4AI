package canvas

import (
	"encoding/json"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func canvasOperationsPayload(canvasID string, existing *model.CanvasProject, req CanvasOperationsRequest) (map[string]json.RawMessage, []CanvasOperation, error) {
	isCreate := existing == nil
	if isCreate && (len(req.Operations) == 0 || req.Operations[0].Type != "create_canvas") {
		return nil, nil, kernel.NotFound("画布不存在或已删除")
	}
	if !isCreate {
		for _, operation := range req.Operations {
			if operation.Type == "create_canvas" {
				return nil, nil, canvasOperationConflict("画布 %q 已存在，不能重复创建", canvasID)
			}
		}
	}
	if req.BaseRevision != nil && *req.BaseRevision > 0 && isCreate {
		return nil, nil, canvasMutationError("canvas_invalid_base", "新画布不能使用已有版本作为基线")
	}
	var payload map[string]json.RawMessage
	var operations []CanvasOperation
	if isCreate {
		create := req.Operations[0]
		if create.ID != "" && create.ID != canvasID {
			return nil, nil, kernel.BadAuthRequest("画布创建操作的 ID 与请求路径不一致")
		}
		payload = map[string]json.RawMessage{
			"id":          jsonRawValue(canvasID),
			"nodes":       json.RawMessage(`[]`),
			"connections": json.RawMessage(`[]`),
		}
		for field, value := range create.Patch {
			if !allowedCanvasOperationField(field) {
				return nil, nil, kernel.BadAuthRequest("不支持的新画布字段")
			}
			payload[field] = append(json.RawMessage(nil), value...)
		}
		operations = req.Operations[1:]
	} else {
		// 客户端的 beforeValue 来自 GET 返回的投影（title/projectId 等以数据库列为准），
		// 字段基线校验必须对同一份投影进行，否则 projectId 列为 "" 而 payload 缺省时会误判冲突。
		base, err := canvasProjectPayload(*existing)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal(base, &payload); err != nil {
			return nil, nil, err
		}
		operations = req.Operations
	}
	return payload, operations, nil
}
