package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// CloudAgentUndoResult is the user-facing result of undoing one Agent run.
type CloudAgentUndoResult struct {
	Revision        int64    `json:"revision"`
	RestoredNodeIDs []string `json:"restoredNodeIds"`
}

type cloudAgentUndoValue struct {
	raw     any
	present bool
}

type cloudAgentUndoEntry struct {
	base, final cloudAgentUndoValue
}

// UndoCloudAgentRun reverts every canvas change one finished Agent run made,
// as a three-way merge: only entities the run changed are restored, so later
// user edits on other nodes survive. When an entity the run changed no longer
// holds the run's final value, someone edited it afterwards; the whole undo is
// refused with agent_undo_conflict and the canvas is not written. Submitted
// generation tasks are never undone, cancelled or refunded here.
func (s *Service) UndoCloudAgentRun(userID, runID, reason string) (*CloudAgentUndoResult, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, Unauthorized("请先登录")
	}
	if strings.TrimSpace(runID) == "" {
		return nil, BadAuthRequest("缺少 Agent 运行 ID")
	}
	if len([]rune(reason)) > 2000 {
		return nil, BadAuthRequest("撤销理由过长")
	}

	s.storageMu.Lock()
	defer s.storageMu.Unlock()

	run, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("Agent 运行不存在")
		}
		return nil, err
	}
	if !cloudAgentRunFinished(run.Status) {
		return nil, creationConflict("Agent 仍在运行，结束后才能撤销本次修改")
	}
	var result *CloudAgentUndoResult
	err = s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		chain, err := repo.CloudAgentCanvasMutationChain(userID, runID, current.CanvasID)
		if err != nil {
			return err
		}
		if len(chain) == 0 {
			return NotFound("本次运行没有可撤销的画布修改")
		}
		for _, mutation := range chain {
			if mutation.HasSubmittedTask {
				return BadAuthRequest("本次运行已提交生成任务，不能撤销；任务不会取消或退款")
			}
			if mutation.Status != "applied" || mutation.BeforeJSON == "" || mutation.AfterJSON == "" {
				return BadAuthRequest("本次运行有画布修改未保留完整快照，无法撤销")
			}
		}
		canvas, err := repo.CanvasProjectForUser(userID, current.CanvasID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return NotFound("画布不存在或无权访问")
			}
			return err
		}
		live, err := creationDocument(canvas.PayloadJSON)
		if err != nil {
			return err
		}
		next, restored, err := cloudAgentUndoDocument(chain, live)
		if err != nil {
			return err
		}
		previous := canvas.PayloadJSON
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		canvas.PayloadJSON = string(raw)
		if err := saveCreationCanvasWithHistory(repo, canvas, previous); err != nil {
			if errors.Is(err, repository.ErrCreationConflict) {
				return kernel.AgentUndoConflict("画布刚刚发生了新的修改，请稍后重试撤销", nil)
			}
			return err
		}
		now := time.Now().UTC()
		for index := len(chain) - 1; index >= 0; index-- {
			if err := repo.MarkCloudAgentCanvasMutationUndone(userID, runID, chain[index].ID, now); err != nil {
				if errors.Is(err, repository.ErrCreationConflict) {
					return kernel.AgentUndoConflict("本次修改已被处理，请重新读取画布", nil)
				}
				return err
			}
		}
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.event(runID, "canvas_undone", map[string]any{
			"canvasId":        current.CanvasID,
			"scope":           "run",
			"revision":        canvas.Revision,
			"restoredNodeIds": restored,
			"reason":          strings.TrimSpace(reason),
		})
		if err := cloudAgentSave(current, &state); err != nil {
			return err
		}
		result = &CloudAgentUndoResult{Revision: canvas.Revision, RestoredNodeIDs: restored}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func cloudAgentRunFinished(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "rejected":
		return true
	}
	return false
}

// cloudAgentUndoDocument folds the run's mutations into, per entity, the value
// before the run first touched it and the value it last left behind, then
// restores the former wherever the live canvas still holds the latter.
// Comparison uses the Agent-visible content (the snapshotHash projection), so
// browser bookkeeping such as node timestamps or measured heights is not a
// conflict. Mutation rows do not record a revision, and those bookkeeping
// saves also bump entity versions, so versions alone cannot decide authorship.
func cloudAgentUndoDocument(chain []model.CloudAgentCanvasMutation, live map[string]any) (map[string]any, []string, error) {
	entries := map[string]*cloudAgentUndoEntry{}
	order := []string{}
	var baseOrder, finalOrder map[string][]string
	for index, mutation := range chain {
		before, err := creationDocument(mutation.BeforeJSON)
		if err != nil {
			return nil, nil, BadAuthRequest("本次运行的撤销快照无效")
		}
		after, err := creationDocument(mutation.AfterJSON)
		if err != nil {
			return nil, nil, BadAuthRequest("本次运行的撤销快照无效")
		}
		if index == 0 {
			baseOrder = cloudAgentUndoOrders(before)
		}
		if index == len(chain)-1 {
			finalOrder = cloudAgentUndoOrders(after)
		}
		beforeEntities, afterEntities := cloudAgentUndoEntities(before), cloudAgentUndoEntities(after)
		keys := map[string]bool{}
		for key := range beforeEntities {
			keys[key] = true
		}
		for key := range afterEntities {
			keys[key] = true
		}
		for key := range keys {
			b, a := beforeEntities[key], afterEntities[key]
			if cloudAgentUndoSame(key, b, a) {
				continue
			}
			entry := entries[key]
			if entry == nil {
				entry = &cloudAgentUndoEntry{base: b}
				entries[key] = entry
				order = append(order, key)
			}
			entry.final = a
		}
	}
	liveEntities := cloudAgentUndoEntities(live)
	changes := commands.ChangeSet{Entities: []commands.EntityChange{}, Fields: map[string]commands.FieldChange{}}
	conflicts, restored := []string{}, []string{}
	sort.Strings(order)
	for _, key := range order {
		entry := entries[key]
		if cloudAgentUndoSame(key, entry.base, entry.final) {
			continue
		}
		if !cloudAgentUndoSame(key, liveEntities[key], entry.final) {
			conflicts = append(conflicts, key)
			continue
		}
		group, id, _ := strings.Cut(key, ":")
		if group == "node" {
			restored = append(restored, id)
		}
		if group == "project" {
			if entry.base.present {
				changes.Fields[id] = commands.FieldChange{Value: entry.base.raw}
			} else {
				changes.Fields[id] = commands.FieldChange{Delete: true}
			}
			continue
		}
		if entry.base.present {
			value, _ := entry.base.raw.(map[string]any)
			changes.Entities = append(changes.Entities, commands.EntityChange{Key: key, Value: value})
		} else {
			changes.Entities = append(changes.Entities, commands.EntityChange{Key: key, Delete: true})
		}
	}
	if len(conflicts) > 0 {
		ids := []string{}
		for _, key := range conflicts {
			_, id, _ := strings.Cut(key, ":")
			ids = append(ids, id)
		}
		return nil, nil, kernel.AgentUndoConflict("Agent 修改过的内容之后又被编辑过，未执行撤销", map[string]any{"entityKeys": conflicts, "ids": ids})
	}
	if len(changes.Entities) == 0 && len(changes.Fields) == 0 {
		return nil, nil, NotFound("本次运行的画布修改已经不存在，无需撤销")
	}
	next, err := commands.ApplyChanges(live, changes)
	if err != nil {
		return nil, nil, err
	}
	// Restore a reorder the run made only while the live canvas still shows it.
	liveOrder := cloudAgentUndoOrders(live)
	for _, field := range []string{"nodes", "connections"} {
		if ordered, ok := cloudAgentUndoRestoreOrder(next[field], baseOrder[field], finalOrder[field], liveOrder[field]); ok {
			next[field] = ordered
		}
	}
	return next, restored, nil
}

func cloudAgentUndoEntities(doc map[string]any) map[string]cloudAgentUndoValue {
	out := map[string]cloudAgentUndoValue{}
	for _, group := range []struct{ field, prefix string }{{"nodes", "node:"}, {"connections", "connection:"}} {
		for _, item := range creationMaps(doc[group.field]) {
			if id := stringValue(item["id"]); id != "" {
				out[group.prefix+id] = cloudAgentUndoValue{raw: item, present: true}
			}
		}
	}
	for key, value := range doc {
		if key == "nodes" || key == "connections" || cloudAgentCanvasUIKeys[key] {
			continue
		}
		out["project:"+key] = cloudAgentUndoValue{raw: value, present: true}
	}
	return out
}

func cloudAgentUndoSame(key string, left, right cloudAgentUndoValue) bool {
	if left.present != right.present {
		return false
	}
	if !left.present {
		return true
	}
	if strings.HasPrefix(key, "node:") {
		return creationHash(cloudAgentUndoNodeContent(left.raw)) == creationHash(cloudAgentUndoNodeContent(right.raw))
	}
	return creationHash(left.raw) == creationHash(right.raw)
}

func cloudAgentUndoNodeContent(node any) any {
	projected := creationMaps(cloudAgentCanvasContent(map[string]any{"nodes": []any{node}})["nodes"])
	if len(projected) != 1 {
		return node
	}
	return projected[0]
}

func cloudAgentUndoOrders(doc map[string]any) map[string][]string {
	out := map[string][]string{}
	for _, field := range []string{"nodes", "connections"} {
		ids := []string{}
		for _, item := range creationMaps(doc[field]) {
			ids = append(ids, stringValue(item["id"]))
		}
		out[field] = ids
	}
	return out
}

// relativeOrder keeps the ids present in both lists, in the order of from.
func cloudAgentUndoRelativeOrder(from, other []string) []string {
	keep := map[string]bool{}
	for _, id := range other {
		keep[id] = true
	}
	out := []string{}
	for _, id := range from {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out
}

func cloudAgentUndoRestoreOrder(items any, base, final, live []string) ([]any, bool) {
	if reflect.DeepEqual(cloudAgentUndoRelativeOrder(base, final), cloudAgentUndoRelativeOrder(final, base)) {
		return nil, false
	}
	if !reflect.DeepEqual(cloudAgentUndoRelativeOrder(live, final), cloudAgentUndoRelativeOrder(final, live)) {
		return nil, false
	}
	rank := map[string]int{}
	for index, id := range base {
		rank[id] = index
	}
	list := creationMaps(items)
	// Base items move back into base order; items absent from base keep their slot.
	positions, ranked := []int{}, []map[string]any{}
	for index, item := range list {
		if _, ok := rank[stringValue(item["id"])]; ok {
			positions = append(positions, index)
			ranked = append(ranked, item)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return rank[stringValue(ranked[i]["id"])] < rank[stringValue(ranked[j]["id"])] })
	out := make([]any, len(list))
	for index, item := range list {
		out[index] = item
	}
	for index, position := range positions {
		out[position] = ranked[index]
	}
	return out, true
}
