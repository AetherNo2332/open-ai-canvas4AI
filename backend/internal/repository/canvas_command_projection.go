package repository

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"yingce/backend/internal/assets"
	"yingce/backend/internal/model"
)

func canvasEpochID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// syncLegacyCanvasWrite 把非命令写入（浏览器整份保存、operations、历史恢复、删除）投影到
// Agent 画布命令的持久事件流：变化的实体/字段写新版本，新纪元携带完整 reset。
// 没有这一步，Agent 命令的目标版本 CAS 看不到用户的手工修改，媒体版本等受管字段也可能被
// 旧保存悄悄改写。必须与画布本身的 revision CAS 处于同一事务。
func (r *Repository) syncLegacyCanvasWrite(before, after *model.CanvasProject, reset bool) error {
	// 只迁移局部表的测试夹具没有命令表；一旦纪元表存在，任一命令表失败都要让保存失败。
	present, err := r.hasCommandSchema()
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	var old, next map[string]any
	if before != nil {
		if err := json.Unmarshal([]byte(before.PayloadJSON), &old); err != nil {
			return err
		}
	}
	if err := json.Unmarshal([]byte(after.PayloadJSON), &next); err != nil {
		return err
	}
	if next == nil {
		return fmt.Errorf("canvas document is null")
	}
	if err := guardLegacyMediaVersions(old, next); err != nil {
		return err
	}
	epoch, err := r.CommandEpoch(after.UserID, after.ID)
	newEpoch := r.IsMissing(err)
	if err != nil && !newEpoch {
		return err
	}
	if newEpoch {
		epoch = &model.CanvasDocumentEpoch{CanvasID: after.ID, OwnerID: after.UserID}
	}
	if newEpoch || reset || (epoch.Deleted && next["_deleted"] != true) {
		epoch.Epoch, err = canvasEpochID()
		if err != nil {
			return err
		}
		newEpoch = true
	}
	epoch.EventSeq++
	epoch.Deleted = next["_deleted"] == true
	epoch.UpdatedAt = time.Now()
	versions, err := r.CommandVersions(after.ID, epoch.Epoch)
	if err != nil {
		return err
	}
	rows := []model.CanvasEntityVersion{}
	changedVersions := map[string]int64{}
	entities := []any{}
	fields := map[string]any{}
	changes := map[string]any{"entities": entities, "fields": fields}
	write := func(key string, deleted bool) {
		v := model.CanvasEntityVersion{CanvasID: after.ID, Epoch: epoch.Epoch, EntityKey: key, Version: after.Revision, Deleted: deleted}
		rows = append(rows, v)
		versions[key] = v
		changedVersions[key] = v.Version
	}
	for _, group := range []struct{ field, prefix string }{{"nodes", "node:"}, {"connections", "connection:"}} {
		index := func(doc map[string]any) (map[string]map[string]any, []string) {
			out, order := map[string]map[string]any{}, []string{}
			list, _ := doc[group.field].([]any)
			for _, raw := range list {
				if value, ok := raw.(map[string]any); ok {
					if id, ok := value["id"].(string); ok {
						out[id] = value
						order = append(order, id)
					}
				}
			}
			return out, order
		}
		prior, priorOrder := index(old)
		current, currentOrder := index(next)
		for id, value := range current {
			if newEpoch || !reflect.DeepEqual(prior[id], value) {
				write(group.prefix+id, false)
				entities = append(entities, map[string]any{"key": group.prefix + id, "value": value})
				if group.field == "nodes" && (value["type"] == "drawing" || prior[id]["type"] == "drawing") {
					write("drawing:"+id, false)
				}
			}
		}
		for id, value := range prior {
			if current[id] == nil {
				write(group.prefix+id, true)
				entities = append(entities, map[string]any{"key": group.prefix + id, "delete": true})
				if value["type"] == "drawing" {
					write("drawing:"+id, true)
				}
			}
		}
		if newEpoch || !reflect.DeepEqual(priorOrder, currentOrder) {
			name := "nodeOrder"
			if group.field == "connections" {
				name = "connectionOrder"
			}
			write("project:"+name, false)
			changes[name] = currentOrder
		}
	}
	for key, value := range next {
		if key != "nodes" && key != "connections" && (newEpoch || !reflect.DeepEqual(old[key], value)) {
			write("project:"+key, false)
			fields[key] = map[string]any{"value": value}
		}
	}
	for key := range old {
		if _, ok := next[key]; !ok && key != "nodes" && key != "connections" {
			write("project:"+key, true)
			fields[key] = map[string]any{"delete": true}
		}
	}
	if err = r.SaveCommandVersions(rows); err != nil {
		return err
	}
	if err = r.fenceLegacyMediaOutputOwners(after.UserID, after.ID, epoch.Epoch, old, next); err != nil {
		return err
	}
	values := map[string]int64{}
	generations, changedGenerations := map[string]int64{}, map[string]int64{}
	for _, value := range rows {
		versions[value.EntityKey] = value
		changedGenerations[value.EntityKey] = value.Generation
	}
	for key, value := range versions {
		values[key] = value.Version
		generations[key] = value.Generation
	}
	op := fmt.Sprintf("legacy:%d:%d", after.Revision, epoch.EventSeq)
	snapshot := map[string]any{"protocolVersion": 1, "canvasId": after.ID, "documentEpoch": epoch.Epoch, "revision": after.Revision, "eventSeq": epoch.EventSeq, "document": next, "entityVersions": values}
	snapshot["entityGenerations"] = generations
	changes["entities"] = entities
	event := map[string]any{"protocolVersion": 1, "canvasId": after.ID, "documentEpoch": epoch.Epoch, "operationId": op, "clientId": "legacy", "actorId": after.UserID, "revision": after.Revision, "eventSeq": epoch.EventSeq, "changes": changes, "entityVersions": changedVersions}
	event["entityGenerations"] = changedGenerations
	if newEpoch || epoch.Deleted {
		event["reset"] = snapshot
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err = r.InsertCommandEvent(&model.CanvasCommandEvent{CanvasID: after.ID, OwnerID: after.UserID, Epoch: epoch.Epoch, Sequence: epoch.EventSeq, OperationID: op, EventJSON: string(encoded), CreatedAt: epoch.UpdatedAt}); err != nil {
		return err
	}
	if err = r.SaveCommandEpoch(epoch); err != nil {
		return err
	}
	refs, err := assets.CollectDocumentResourceReferences(after.PayloadJSON)
	if err != nil {
		return err
	}
	current := []string{}
	if !epoch.Deleted {
		for _, ref := range refs {
			current = append(current, ref.ResourceID)
		}
	}
	// 非命令事件只用于同步，不用于撤销。快照资源有自己的有界保留；只有真正的命令历史持有撤销引用。
	if err = r.SaveCommandResources(after.UserID, after.ID, epoch.Epoch, op, current, nil); err != nil {
		return err
	}
	return r.releaseOrphanLegacyCommandResources(after.UserID, after.ID)
}

func (r *Repository) releaseOrphanLegacyCommandResources(ownerID, canvasID string) error {
	retained := r.db.Model(&model.CanvasActorHistory{}).Select("1").
		Where("canvas_actor_histories.owner_id = canvas_command_resources.owner_id AND canvas_actor_histories.canvas_id = canvas_command_resources.canvas_id AND canvas_actor_histories.epoch = canvas_command_resources.epoch AND canvas_actor_histories.operation_id = canvas_command_resources.operation_id")
	return r.db.Where("owner_id = ? AND canvas_id = ? AND kind = ? AND operation_id LIKE ?", ownerID, canvasID, "history", "legacy:%").
		Where("NOT EXISTS (?)", retained).Delete(&model.CanvasCommandResource{}).Error
}
