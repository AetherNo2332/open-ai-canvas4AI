package tasks

import (
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/model"
)

func validateConsumer(node map[string]any, req SubmitRequest) error {
	kind := req.Consumer.Kind
	if kind == "batch_document" {
		if str(node["type"]) != "text" || req.Task.Mode != "text" || req.Task.Operation != "list_analysis" {
			return commands.BadAuthRequest("列表分析任务与目标不符")
		}
		return nil
	}
	if kind == "storyboard_document" {
		if str(node["type"]) != "script" || req.Task.Mode != "text" || req.Task.Operation != "storyboard" {
			return commands.BadAuthRequest("专业拆镜任务与目标不符")
		}
		return nil
	}
	if kind == "" || kind == "node" {
		if str(node["type"]) != req.Task.Mode {
			return commands.BadAuthRequest("生成模式与目标节点不符")
		}
		return nil
	}
	if kind != "batch" && kind != "storyboard" {
		return commands.BadAuthRequest("结果消费类型无效")
	}
	if req.Consumer.RowID == "" {
		return commands.BadAuthRequest("结构化生成缺少行 ID")
	}
	if kind == "batch" {
		table, _ := metadata(node)["batchTable"].(map[string]any)
		if table == nil || str(node["type"]) != "batch-table" {
			return commands.BadAuthRequest("批量目标无效")
		}
		for _, row := range objectList(table["rows"]) {
			if str(row["id"]) == req.Consumer.RowID {
				if req.Task.Mode != "image" || req.Consumer.OutputNodeID == "" || str(row["outputNodeId"]) != req.Consumer.OutputNodeID {
					return commands.BadAuthRequest("批量行输出绑定不符")
				}
				return nil
			}
		}
	} else {
		board, _ := metadata(node)["storyboard"].(map[string]any)
		if board == nil || str(node["type"]) != "script" {
			return commands.BadAuthRequest("分镜目标无效")
		}
		if (req.Task.Mode == "image" && req.Consumer.Field != "imageNodeId") || (req.Task.Mode == "video" && req.Consumer.Field != "videoNodeId") || (req.Task.Mode == "text" && req.Consumer.Field != "dialogue" && req.Consumer.Field != "videoMotionPrompt" && req.Consumer.Field != "imageGenerationPrompt") {
			return commands.BadAuthRequest("分镜结果字段无效")
		}
		for _, row := range objectList(board["rows"]) {
			if str(row["id"]) == req.Consumer.RowID {
				if req.Task.Mode != "text" && (req.Consumer.OutputNodeID == "" || str(row[req.Consumer.Field]) != req.Consumer.OutputNodeID) {
					return commands.BadAuthRequest("分镜输出绑定不符")
				}
				return nil
			}
		}
	}
	return commands.BadAuthRequest("结构化目标行不存在")
}
func consumerRow(node map[string]any, c Consumer) map[string]any {
	key := "storyboard"
	if c.Kind == "batch" {
		key = "batchTable"
	}
	table, _ := metadata(node)[key].(map[string]any)
	for _, row := range objectList(table["rows"]) {
		if str(row["id"]) == c.RowID {
			return row
		}
	}
	return nil
}
func setPending(node map[string]any, req SubmitRequest, task model.Task) {
	m := metadata(node)
	if req.Consumer.Kind == "batch" || req.Consumer.Kind == "storyboard" {
		row := consumerRow(node, req.Consumer)
		row["taskId"], row["status"] = task.ID, "loading"
		row["taskStatus"] = string(task.Status)
		return
	}
	m["taskId"], m["taskStatus"], m["taskProgress"], m["status"], m["prompt"] = task.ID, string(task.Status), 5, "loading", task.Prompt
	delete(m, "errorDetails")
}
