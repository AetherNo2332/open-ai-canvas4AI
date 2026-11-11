package document

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/agentmemory"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

var sectionLabels = map[string]string{
	model.AgentMemorySectionUser:  "关于用户",
	model.AgentMemorySectionNotes: "工作笔记",
}

// ModeOf 归一化记忆模式；未知值按 auto 处理。
func ModeOf(mode string) string {
	switch strings.TrimSpace(mode) {
	case model.AgentMemoryDocumentModeReview:
		return model.AgentMemoryDocumentModeReview
	case model.AgentMemoryDocumentModeOff:
		return model.AgentMemoryDocumentModeOff
	default:
		return model.AgentMemoryDocumentModeAuto
	}
}

// CapacityOf 返回文档显式容量；未配置时用默认值。
func CapacityOf(doc *model.AgentMemoryDocument) int {
	if doc == nil || doc.Capacity <= 0 {
		return DefaultCapacity
	}
	return doc.Capacity
}

// DocumentEntries 解码文档的条目列表；空文档返回空列表。
func DocumentEntries(doc *model.AgentMemoryDocument) ([]model.AgentMemoryEntry, error) {
	entries := []model.AgentMemoryEntry{}
	if doc == nil || strings.TrimSpace(doc.EntriesJSON) == "" {
		return entries, nil
	}
	if err := json.Unmarshal([]byte(doc.EntriesJSON), &entries); err != nil {
		return nil, fmt.Errorf("decode Agent memory document: %w", err)
	}
	if entries == nil {
		entries = []model.AgentMemoryEntry{}
	}
	return entries, nil
}

// EntriesFromJSON 宽松解码条目列表；解析失败返回空列表（修订快照展示用）。
func EntriesFromJSON(raw string) []model.AgentMemoryEntry {
	entries := []model.AgentMemoryEntry{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &entries)
	}
	if entries == nil {
		entries = []model.AgentMemoryEntry{}
	}
	return entries
}

// EntriesJSON 编码条目列表；nil 写成空数组，保证前后快照 JSON 稳定。
func EntriesJSON(entries []model.AgentMemoryEntry) (string, error) {
	if entries == nil {
		entries = []model.AgentMemoryEntry{}
	}
	raw, err := json.Marshal(entries)
	return string(raw), err
}

// NextSeq 取文档计数与现有 ID 最大序号中较大者。
func NextSeq(doc *model.AgentMemoryDocument, entries []model.AgentMemoryEntry) int64 {
	if doc == nil {
		return agentmemory.NextSeq(0, entries)
	}
	return agentmemory.NextSeq(doc.NextEntrySeq, entries)
}

func entryIndex(entries []model.AgentMemoryEntry, id string) int {
	for index := range entries {
		if entries[index].ID == id {
			return index
		}
	}
	return -1
}

func section(raw, label string) (string, error) {
	name := strings.TrimSpace(raw)
	if _, ok := sectionLabels[name]; !ok {
		return "", kernel.BadAuthRequest(label + " 必须是 user（关于用户）或 notes（工作笔记）")
	}
	return name, nil
}

// ApplyOperations 按顺序执行一批修改，只按整批结果检查容量：模型可以在同一次调用里
// 先合并或删除旧条目再新增。与已有条目重复的新增计为 duplicates，不报错也不写入。
func ApplyOperations(current []model.AgentMemoryEntry, nextSeq int64, ops []Operation, capacity int, now time.Time, source, runID string) (Edit, error) {
	edit := Edit{NextSeq: nextSeq}
	if len(ops) == 0 {
		return edit, kernel.BadAuthRequest("operations 不能为空")
	}
	if len(ops) > OpsMax {
		return edit, kernel.BadAuthRequest(fmt.Sprintf("一次最多 %d 个修改，请合并后再提交", OpsMax))
	}
	entries := append([]model.AgentMemoryEntry(nil), current...)
	for i, op := range ops {
		label := fmt.Sprintf("operations[%d]", i)
		id := strings.TrimSpace(op.ID)
		switch strings.TrimSpace(op.Action) {
		case "add":
			name, err := section(op.Section, label+".section")
			if err != nil {
				return edit, err
			}
			text, err := agentmemory.ValidateText(op.Text, label+".text")
			if err != nil {
				return edit, err
			}
			if agentmemory.Duplicate(entries, text, "") {
				edit.Duplicates++
				continue
			}
			id = agentmemory.EntryID(name, edit.NextSeq)
			edit.NextSeq++
			entries = append(entries, model.AgentMemoryEntry{ID: id, Section: name, Text: text, Source: source, OriginRunID: runID, CreatedAt: now, UpdatedAt: now})
			edit.Changes = append(edit.Changes, Change{Kind: "add", ID: id, Section: name, After: text})
		case "replace":
			at := entryIndex(entries, id)
			if at < 0 {
				return edit, kernel.BadAuthRequest(label + ".id 不存在：只能使用记忆快照或本轮工具结果里出现过的条目 ID")
			}
			text, err := agentmemory.ValidateText(op.Text, label+".text")
			if err != nil {
				return edit, err
			}
			name := entries[at].Section
			if strings.TrimSpace(op.Section) != "" {
				if name, err = section(op.Section, label+".section"); err != nil {
					return edit, err
				}
			}
			if entries[at].Text == text && entries[at].Section == name {
				continue
			}
			edit.Changes = append(edit.Changes, Change{Kind: "replace", ID: id, Section: name, Before: entries[at].Text, After: text})
			entries[at].Text, entries[at].Section, entries[at].UpdatedAt = text, name, now
		case "remove":
			at := entryIndex(entries, id)
			if at < 0 {
				return edit, kernel.BadAuthRequest(label + ".id 不存在：只能删除记忆快照或本轮工具结果里出现过的条目")
			}
			edit.Changes = append(edit.Changes, Change{Kind: "remove", ID: id, Section: entries[at].Section, Before: entries[at].Text})
			entries = append(entries[:at:at], entries[at+1:]...)
		default:
			return edit, kernel.BadAuthRequest(label + ".action 只能是 add、replace 或 remove")
		}
	}
	if len(entries) > EntriesMax {
		return edit, kernel.BadAuthRequest(fmt.Sprintf("记忆最多 %d 条，请先合并相近条目", EntriesMax))
	}
	if used, after := agentmemory.CharCount(current), agentmemory.CharCount(entries); after > capacity && after > used {
		return edit, &CapacityError{Used: used, After: after, Capacity: capacity}
	}
	edit.Entries = entries
	return edit, nil
}

// Diff 按条目 ID 比较两个版本，供修订展示和撤销使用。
func Diff(before, after []model.AgentMemoryEntry) []Change {
	previous := make(map[string]model.AgentMemoryEntry, len(before))
	for _, entry := range before {
		previous[entry.ID] = entry
	}
	changes := []Change{}
	seen := make(map[string]bool, len(after))
	for _, entry := range after {
		seen[entry.ID] = true
		old, existed := previous[entry.ID]
		switch {
		case !existed:
			changes = append(changes, Change{Kind: "add", ID: entry.ID, Section: entry.Section, After: entry.Text})
		case old.Text != entry.Text || old.Section != entry.Section:
			changes = append(changes, Change{Kind: "replace", ID: entry.ID, Section: entry.Section, Before: old.Text, After: entry.Text})
		}
	}
	for _, entry := range before {
		if !seen[entry.ID] {
			changes = append(changes, Change{Kind: "remove", ID: entry.ID, Section: entry.Section, Before: entry.Text})
		}
	}
	return changes
}

// Summary 汇总一批变化并附上截断后的修改原因。
func Summary(changes []Change, reason string) string {
	counts := map[string]int{}
	for _, change := range changes {
		counts[change.Kind]++
	}
	parts := []string{}
	for _, item := range []struct{ kind, label string }{{"add", "新增"}, {"replace", "改写"}, {"remove", "删除"}} {
		if counts[item.kind] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d 条", item.label, counts[item.kind]))
		}
	}
	text := strings.Join(parts, "、")
	if reason = agentmemory.NormalizeText(reason); reason != "" {
		text += "：" + agentmemory.TruncateRunes(reason, ReasonMaxRunes)
	}
	return kernel.TruncateRunes(text, 480)
}

// Inverse 只回退一条修订涉及的条目，并要求它们仍是修订后的样子；之后又被改过的条目不覆盖。
func Inverse(current, before, after []model.AgentMemoryEntry, now time.Time) ([]model.AgentMemoryEntry, error) {
	previous := make(map[string]model.AgentMemoryEntry, len(before))
	for _, entry := range before {
		previous[entry.ID] = entry
	}
	next := append([]model.AgentMemoryEntry(nil), current...)
	for _, change := range Diff(before, after) {
		at := entryIndex(next, change.ID)
		switch change.Kind {
		case "add":
			if at < 0 {
				continue
			}
			if next[at].Text != change.After {
				return nil, ErrUndoConflict
			}
			next = append(next[:at:at], next[at+1:]...)
		case "replace":
			if at < 0 || next[at].Text != change.After || next[at].Section != change.Section {
				return nil, ErrUndoConflict
			}
			old := previous[change.ID]
			next[at].Text, next[at].Section, next[at].UpdatedAt = old.Text, old.Section, now
		case "remove":
			if at >= 0 {
				return nil, ErrUndoConflict
			}
			next = append(next, previous[change.ID])
		}
	}
	return next, nil
}

// ErrUndoConflict 表示撤销时相关条目已被后续修改，无法自动回退。
var ErrUndoConflict = errors.New("memory changed after revision")
