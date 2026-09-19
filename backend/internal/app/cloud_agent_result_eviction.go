package app

import "strings"

// 工具结果的"正文类"键。它们都能通过重新读取拿回，因此是正文卸载的对象；
// 除此之外的键（节点 ID、快照哈希、任务绑定、审批、错误、生成状态、迁移提示…）
// 一律原样保留：卸载只负责"把可再生的正文移出上下文"，不做摘要、不改事实。
var cloudAgentEvictableResultKeys = []string{
	"content", "nodes", "rows", "ops", "actions", "preview", "before", "after", "canvasPatch",
}

// cloudAgentResultSkeletonLimit 是骨架里保留的行号数量上限：保留少量行号让模型知道
// "有哪些行"（省掉一次只为确认存在性的重读），超出则只给计数。
const cloudAgentResultSkeletonLimit = 60

// cloudAgentEvictResultBody 把一条工具结果里的正文换成紧凑骨架，返回是否发生变化。
//
// 分镜与批量表是最容易把上下文吃满的读取结果（实测一次 51 行分镜的精读单条就 10KB 以上），
// 但它们的正文全部可以重新读取，所以这里只保留"规模 + 行号/页码 + 哈希"：
//   - storyboard → {totalRows, shotNumbers(截断), hasMore, nextOffset}
//   - batchTable → {totalRows, hasMore, nextOffset, operation}
//   - rows（顶层）→ 只留计数
func cloudAgentEvictResultBody(result map[string]any) bool {
	changed := false
	if storyboard, ok := result["storyboard"].(map[string]any); ok {
		skeleton := map[string]any{}
		rows := cloudAgentRowsOf(storyboard["rows"])
		skeleton["totalRows"] = len(rows)
		if numbers := cloudAgentShotNumbers(rows); len(numbers) > 0 {
			skeleton["shotNumbers"] = numbers
		}
		for _, key := range []string{"hasMore", "nextOffset", "totalRows"} {
			if value, exists := storyboard[key]; exists {
				skeleton[key] = value
			}
		}
		result["storyboard"] = skeleton
		changed = true
	}
	if table, ok := result["batchTable"].(map[string]any); ok {
		skeleton := map[string]any{}
		rows := cloudAgentRowsOf(table["rows"])
		skeleton["totalRows"] = len(rows)
		for _, key := range []string{"hasMore", "nextOffset", "operation", "concurrency", "generationPreview"} {
			if value, exists := table[key]; exists {
				skeleton[key] = value
			}
		}
		result["batchTable"] = skeleton
		changed = true
	}
	if rows := cloudAgentRowsOf(result["rows"]); len(rows) > 0 {
		result["rowsOmitted"] = len(rows)
		delete(result, "rows")
		changed = true
	}
	for _, key := range cloudAgentEvictableResultKeys {
		if _, exists := result[key]; !exists {
			continue
		}
		delete(result, key)
		changed = true
	}
	return changed
}

func cloudAgentRowsOf(value any) []map[string]any {
	switch items := value.(type) {
	case []any:
		rows := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if row, ok := item.(map[string]any); ok {
				rows = append(rows, row)
			}
		}
		return rows
	case []map[string]any:
		return items
	default:
		return nil
	}
}

// cloudAgentShotNumbers 优先给镜号（导演看得懂），没有镜号时退回行 ID 前 8 位。
func cloudAgentShotNumbers(rows []map[string]any) []any {
	numbers := make([]any, 0, min(len(rows), cloudAgentResultSkeletonLimit))
	for _, row := range rows {
		if len(numbers) == cloudAgentResultSkeletonLimit {
			break
		}
		if shot, ok := cloudAgentSafeNumber(row["shotNumber"]); ok {
			numbers = append(numbers, shot)
			continue
		}
		if number := strings.TrimSpace(stringValue(row["shotNumber"])); number != "" {
			numbers = append(numbers, truncateRunes(number, 12))
			continue
		}
		if id := strings.TrimSpace(stringValue(row["id"])); id != "" {
			numbers = append(numbers, truncateRunes(id, 8))
		}
	}
	return numbers
}

// cloudAgentEvictionGuidance 是给模型的固定提示：正文已移出，需要时重新读取，
// 且历史状态不是当前状态、也不构成执行授权。
const cloudAgentEvictionGuidance = "历史读取正文已移出模型上下文；需要时重新读取。保留的历史状态不是当前状态，也不是执行授权，不得据此重复提交生成。"
