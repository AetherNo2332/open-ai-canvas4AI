package commands

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/canvas/capability"
)

// StoryboardSplitScope 是一次拆镜任务的写回范围，提交时由服务端冻结在任务绑定里。
// RowIDs 为空表示整表拆镜；非空表示局部重拆，必须是表中连续的一段未锁定行。
type StoryboardSplitScope struct {
	RowIDs []string `json:"rowIds,omitempty"`
}

// StoryboardRowMapping 是写回后新行与被替换旧行的对应关系，回执用它说明改了哪几行。
type StoryboardRowMapping struct {
	RowID     string   `json:"rowId"`
	Replaced  []string `json:"replaced,omitempty"`
	KeptMedia bool     `json:"keptMedia,omitempty"`
}

// StoryboardSplitResult 描述一次拆镜写回的实际影响。
type StoryboardSplitResult struct {
	Rows          []StoryboardRowMapping `json:"rows"`
	UnboundMedia  []string               `json:"unboundMedia,omitempty"`
	ReviewRowIDs  []string               `json:"reviewRowIds,omitempty"`
	LockedRowIDs  []string               `json:"lockedRowIds,omitempty"`
	ReplacedCount int                    `json:"replacedCount"`
}

const storyboardMediaReviewPending = "pending"

// storyboardRowMediaFields 是分镜行上指向已生成媒体的字段；拆镜改写行时按这些字段保留或解绑媒体。
var storyboardRowMediaFields = []string{"imageNodeId", "videoNodeId"}

// storyboardRowLocked 判断该行是否被用户锁定：锁定行不参与任何拆镜或重拆。
func storyboardRowLocked(row map[string]any) bool {
	locked, _ := row["locked"].(bool)
	return locked
}

// PlanStoryboardSplitScope 在提交时校验并冻结重拆范围：行必须存在、未锁定，且在表中连续。
// 整表拆镜（rowIds 为空）时，锁定行原样保留，其余行由新结果替换。
func PlanStoryboardSplitScope(doc Document, nodeID string, rowIDs []string) (StoryboardSplitScope, error) {
	p := &planner{registry: capability.BuiltinRegistry(), now: func() time.Time { return time.Unix(0, 0) }}
	_, _, rows, err := p.storyboardNodeFromDocument(doc, nodeID)
	if err != nil {
		return StoryboardSplitScope{}, err
	}
	if len(rowIDs) == 0 {
		return StoryboardSplitScope{}, nil
	}
	indexes, err := storyboardScopeIndexes(rows, rowIDs)
	if err != nil {
		return StoryboardSplitScope{}, err
	}
	ordered := make([]string, 0, len(indexes))
	for _, index := range indexes {
		ordered = append(ordered, text(rows[index]["id"]))
	}
	return StoryboardSplitScope{RowIDs: ordered}, nil
}

// storyboardScopeIndexes 返回范围内各行在表中的下标（升序），并要求它们存在、未锁定且连续。
func storyboardScopeIndexes(rows []map[string]any, rowIDs []string) ([]int, error) {
	position := make(map[string]int, len(rows))
	for index, row := range rows {
		position[text(row["id"])] = index
	}
	seen := map[string]bool{}
	indexes := make([]int, 0, len(rowIDs))
	for _, id := range rowIDs {
		id = strings.TrimSpace(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		index, ok := position[id]
		if !ok {
			return nil, BadAuthRequest("重拆范围包含不存在的镜头行：" + id)
		}
		if storyboardRowLocked(rows[index]) {
			return nil, BadAuthRequest(fmt.Sprintf("镜头 %v 已锁定，不能重拆；请先解锁或把它移出范围", rows[index]["shotNumber"]))
		}
		indexes = append(indexes, index)
	}
	if len(indexes) == 0 {
		return nil, BadAuthRequest("重拆范围不能为空")
	}
	sortInts(indexes)
	for i := 1; i < len(indexes); i++ {
		if indexes[i] != indexes[i-1]+1 {
			return nil, BadAuthRequest("局部重拆只能选择连续的一段镜头")
		}
	}
	return indexes, nil
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

// ApplyStoryboardSplit 把拆镜任务结果写回分镜表，并按冻结的范围处理行身份与媒体：
//   - 范围外的行与锁定行原样保留（ID 与已绑定媒体不变）；
//   - 局部重拆且新旧行数相同：一一对应保留旧行 ID 与媒体，媒体标为待复核；
//   - 行数不同（拆分或合并）或整表拆镜：新行用新 ID，被替换行的媒体节点只解绑、不删除。
func ApplyStoryboardSplit(doc Document, nodeID, taskID, raw string, scope StoryboardSplitScope) (Document, StoryboardSplitResult, error) {
	result := StoryboardSplitResult{}
	next, err := cloneDocument(doc)
	if err != nil {
		return doc, result, err
	}
	p := &planner{registry: capability.BuiltinRegistry(), now: func() time.Time { return time.Unix(0, 0) }}
	node, board, old, err := p.storyboardNodeFromDocument(next, nodeID)
	if err != nil {
		return doc, result, err
	}
	payload, err := storyboardPayload(raw, 0)
	if err != nil {
		return doc, result, err
	}
	generated, err := p.storyboardRowsFromPayload(next, payload, taskID)
	if err != nil {
		return doc, result, err
	}
	var rows []map[string]any
	var replaced []map[string]any
	if len(scope.RowIDs) == 0 {
		rows, replaced, result = mergeWholeStoryboard(old, generated)
	} else {
		indexes, scopeErr := storyboardScopeIndexes(old, scope.RowIDs)
		if scopeErr != nil {
			return doc, result, scopeErr
		}
		rows, replaced, result = mergeStoryboardRange(old, generated, indexes[0], indexes[len(indexes)-1])
	}
	if len(rows) == 0 || len(rows) > maxCanvasStoryboardRows {
		return doc, result, BadAuthRequest("分镜写回后必须有 1 至 100 个镜头")
	}
	for index, row := range rows {
		row["shotNumber"] = float64(index + 1)
	}
	board["rows"] = p.mapsAsAny(rows)
	columns := []any{}
	for _, column := range stringList(board["visibleColumns"]) {
		columns = append(columns, column)
	}
	board["visibleColumns"] = capability.NormalizeStoryboardColumns(columns)
	if len(scope.RowIDs) == 0 {
		if title := strings.TrimSpace(text(payload["title"])); title != "" {
			if len([]rune(title)) > 240 {
				return doc, result, BadAuthRequest("分镜标题过长")
			}
			node["title"] = title
		}
	}
	// 被替换的行不再存在：去掉指向它们的行级连线；媒体节点本身留在画布上。
	removed := map[string]bool{}
	for _, row := range replaced {
		removed[text(row["id"])] = true
	}
	edges := []map[string]any{}
	for _, edge := range maps(next["connections"]) {
		if (text(edge["fromNodeId"]) == nodeID || text(edge["toNodeId"]) == nodeID) && removed[text(edge["storyboardRowId"])] {
			continue
		}
		edges = append(edges, edge)
	}
	next["connections"] = p.mapsAsAny(edges)
	if err = validateResult(next); err != nil {
		return doc, result, err
	}
	return next, result, nil
}

// storyboardRowsFromPayload 把模型返回的行校验并补全为标准分镜行；行 ID 由任务派生，重放不会生成第二套行。
func (p *planner) storyboardRowsFromPayload(doc Document, payload map[string]any, taskID string) ([]map[string]any, error) {
	source := maps(payload["rows"])
	if len(source) == 0 || len(source) > maxCanvasStoryboardRows {
		return nil, BadAuthRequest("分镜任务必须返回 1 至 100 个真实镜头")
	}
	fields := capability.StoryboardRowFields()
	rows := make([]map[string]any, 0, len(source))
	for index, row := range source {
		patch := map[string]any{"durationSeconds": float64(6)}
		for key, field := range fields {
			if value, ok := row[key]; ok {
				if field.Kind == "string" {
					patch[key] = normalizeStoryboardText(value, 0)
				} else {
					patch[key] = value
				}
			}
		}
		if err := p.validateCanvasStoryboardRow(patch, true); err != nil {
			return nil, err
		}
		if err := p.validateCanvasStoryboardReferences(doc, patch); err != nil {
			return nil, err
		}
		result := p.canvasStoryboardRowDefaults()
		for key, value := range patch {
			result[key] = value
		}
		result["id"] = "shot-" + taskID + "-" + strconv.Itoa(index+1)
		rows = append(rows, result)
	}
	return rows, nil
}

// mergeWholeStoryboard 整表拆镜：锁定行留在原位置，其余位置按顺序填入新行；
// 新行多于空位时追加在末尾，少于空位时多出的空位删除。
func mergeWholeStoryboard(old, generated []map[string]any) ([]map[string]any, []map[string]any, StoryboardSplitResult) {
	result := StoryboardSplitResult{}
	rows := make([]map[string]any, 0, len(generated)+len(old))
	replaced := []map[string]any{}
	next := 0
	for _, row := range old {
		if storyboardRowLocked(row) {
			rows = append(rows, row)
			result.LockedRowIDs = append(result.LockedRowIDs, text(row["id"]))
			continue
		}
		replaced = append(replaced, row)
		if next < len(generated) {
			rows = append(rows, generated[next])
			result.Rows = append(result.Rows, StoryboardRowMapping{RowID: text(generated[next]["id"]), Replaced: []string{text(row["id"])}})
			next++
		}
	}
	for ; next < len(generated); next++ {
		rows = append(rows, generated[next])
		result.Rows = append(result.Rows, StoryboardRowMapping{RowID: text(generated[next]["id"])})
	}
	result.ReplacedCount = len(replaced)
	result.UnboundMedia = storyboardRowsMedia(replaced)
	return rows, replaced, result
}

// mergeStoryboardRange 局部重拆：只替换 [first, last] 这段行。
// 新旧行数相同且一一对应时保留旧行身份和媒体并标待复核；否则全部换成新行，旧媒体解绑。
func mergeStoryboardRange(old, generated []map[string]any, first, last int) ([]map[string]any, []map[string]any, StoryboardSplitResult) {
	result := StoryboardSplitResult{}
	span := old[first : last+1]
	rows := make([]map[string]any, 0, len(old)-len(span)+len(generated))
	rows = append(rows, old[:first]...)
	replaced := []map[string]any{}
	if len(generated) == len(span) {
		for index, fresh := range generated {
			previous := span[index]
			kept := keepStoryboardRowIdentity(previous, fresh)
			mapping := StoryboardRowMapping{RowID: text(kept["id"]), Replaced: []string{text(previous["id"])}}
			if storyboardRowHasMedia(kept) && storyboardRowTextChanged(previous, kept) {
				kept["mediaReview"] = storyboardMediaReviewPending
				mapping.KeptMedia = true
				result.ReviewRowIDs = append(result.ReviewRowIDs, text(kept["id"]))
			} else if storyboardRowHasMedia(kept) {
				mapping.KeptMedia = true
			}
			rows = append(rows, kept)
			result.Rows = append(result.Rows, mapping)
		}
	} else {
		replaced = append(replaced, span...)
		ids := make([]string, 0, len(span))
		for _, row := range span {
			ids = append(ids, text(row["id"]))
		}
		for index, fresh := range generated {
			mapping := StoryboardRowMapping{RowID: text(fresh["id"])}
			if index == 0 {
				mapping.Replaced = ids
			}
			rows = append(rows, fresh)
			result.Rows = append(result.Rows, mapping)
		}
		result.UnboundMedia = storyboardRowsMedia(span)
	}
	rows = append(rows, old[last+1:]...)
	result.ReplacedCount = len(span)
	return rows, replaced, result
}

// keepStoryboardRowIdentity 用新内容覆盖旧行的创作字段，保留旧行的身份、媒体、任务状态和锁定之外的运行字段。
func keepStoryboardRowIdentity(previous, fresh map[string]any) map[string]any {
	kept := map[string]any{}
	for key, value := range fresh {
		kept[key] = value
	}
	for _, key := range append([]string{"id", "taskId", "taskStatus", "status", "errorDetails", "previsSceneId", "previsSnapshotUrl", "sourceStartMs", "sourceEndMs", "keyframeTimeMs", "mediaReview"}, storyboardRowMediaFields...) {
		if value, ok := previous[key]; ok {
			kept[key] = value
		} else {
			delete(kept, key)
		}
	}
	return kept
}

func storyboardRowHasMedia(row map[string]any) bool {
	for _, field := range storyboardRowMediaFields {
		if strings.TrimSpace(text(row[field])) != "" {
			return true
		}
	}
	return false
}

// storyboardRowTextChanged 比较会影响画面的创作字段；只改了不影响画面的字段时不需要复核媒体。
// 旧行可能缺少新行默认补齐的字段（空字符串、空数组），缺失与空值视为相同。
func storyboardRowTextChanged(previous, next map[string]any) bool {
	for _, field := range canvasStoryboardTextFields {
		if strings.TrimSpace(text(previous[field])) != strings.TrimSpace(text(next[field])) {
			return true
		}
	}
	if fmt.Sprint(previous["durationSeconds"]) != fmt.Sprint(next["durationSeconds"]) {
		return true
	}
	for _, field := range []string{"assetBindings", "characters", "mustHave"} {
		if storyboardListJSON(previous[field]) != storyboardListJSON(next[field]) {
			return true
		}
	}
	return false
}

func storyboardListJSON(value any) string {
	if list, ok := value.([]any); !ok || len(list) == 0 {
		return "[]"
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func storyboardRowsMedia(rows []map[string]any) []string {
	media := []string{}
	for _, row := range rows {
		for _, field := range storyboardRowMediaFields {
			if id := strings.TrimSpace(text(row[field])); id != "" {
				media = append(media, id)
			}
		}
	}
	return media
}
