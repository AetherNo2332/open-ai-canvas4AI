package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// CanvasWriteQuota runs after the account lock, before any document write.
// The supplied repository uses the caller's transaction.
type CanvasWriteQuota func(*Repository, string, bool, int64) error

type StructuredWriteQuota func(tx *Repository, ownerID, kind string, countDelta, byteDelta int64) error

type canvasWriteQuotaContextKey struct{}

// Configure only at application construction, before sharing the repository.
// GORM preserves this context in Transaction/SavePoint, including New(tx).
func (r *Repository) SetCanvasWriteQuota(check CanvasWriteQuota) {
	r.SetStructuredWriteQuota(func(tx *Repository, ownerID, kind string, countDelta, byteDelta int64) error {
		if kind != "canvas" {
			return nil
		}
		return check(tx, ownerID, countDelta > 0, byteDelta)
	})
}

func (r *Repository) SetStructuredWriteQuota(check StructuredWriteQuota) {
	r.structuredWriteQuota = check
	r.db = r.db.WithContext(context.WithValue(r.db.Statement.Context, canvasWriteQuotaContextKey{}, check))
}

func (r *Repository) transactionRepository(db *gorm.DB) *Repository {
	copy := *r
	copy.db = db.WithContext(context.WithValue(db.Statement.Context, canvasWriteQuotaContextKey{}, r.structuredWriteQuota))
	return &copy
}

// LockCanvasWrite always locks the account first, including canvas creation.
// A no-op UPDATE also acquires SQLite's write reservation before any reads,
// avoiding a deferred read transaction's unsafe upgrade under WAL.
func (r *Repository) LockCanvasWrite(ownerID, canvasID string) error {
	if err := r.db.Model(&model.User{}).Where("id = ?", ownerID).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
		return err
	}
	if canvasID != "" {
		return r.db.Model(&model.CanvasProject{}).Where("user_id = ? AND id = ?", ownerID, canvasID).UpdateColumn("revision", gorm.Expr("revision")).Error
	}
	return nil
}

func (r *Repository) checkCanvasWriteQuota(before, after *model.CanvasProject) error {
	if err := r.checkCanvasAuthorLeases(before, after); err != nil {
		return err
	}
	if r.structuredWriteQuota == nil {
		return nil
	}
	delta := int64(len(after.PayloadJSON))
	if before != nil {
		delta -= int64(len(before.PayloadJSON))
	}
	countDelta := int64(0)
	if before == nil {
		countDelta = 1
	}
	return r.structuredWriteQuota(r, after.UserID, "canvas", countDelta, delta)
}

func checkCanvasRevision(before, after *model.CanvasProject) error {
	if after.Revision < 0 || (before == nil && after.Revision != 0) || (before != nil && before.Revision != after.Revision) {
		return ErrCanvasRevisionConflict
	}
	return nil
}

func (r *Repository) checkAssetWriteQuota(before, after *model.Asset) error {
	if r.structuredWriteQuota == nil {
		return nil
	}
	delta, countDelta := int64(len(after.PayloadJSON)), int64(1)
	if before != nil {
		delta -= int64(len(before.PayloadJSON))
		countDelta = 0
	}
	return r.structuredWriteQuota(r, after.UserID, "asset", countDelta, delta)
}

func (r *Repository) guardAssetPersistence(asset *model.Asset, createOnly bool) error {
	if err := r.LockCanvasWrite(asset.UserID, ""); err != nil {
		return err
	}
	before, err := r.AssetForUser(asset.UserID, asset.ID)
	if err != nil && !r.IsMissing(err) {
		return err
	}
	if createOnly && before != nil {
		return nil
	}
	return r.checkAssetWriteQuota(before, asset)
}

func (r *Repository) CanvasStructuredStorageUsage(ownerID string) (UserStorageUsage, error) {
	var usage UserStorageUsage
	if err := r.db.Model(&model.CanvasProject{}).Where("user_id = ?", ownerID).Count(&usage.CanvasCount).Error; err != nil {
		return usage, err
	}
	if err := r.db.Model(&model.Asset{}).Where("user_id = ?", ownerID).Count(&usage.AssetCount).Error; err != nil {
		return usage, err
	}
	expression := "COALESCE(SUM(length(CAST(payload_json AS BLOB))), 0)"
	if r.Dialect() == "postgres" {
		expression = "COALESCE(SUM(octet_length(payload_json)), 0)"
	}
	if err := r.db.Model(&model.CanvasProject{}).Select(expression).Where("user_id = ?", ownerID).Scan(&usage.CanvasBytes).Error; err != nil {
		return usage, err
	}
	err := r.db.Model(&model.Asset{}).Select(expression).Where("user_id = ?", ownerID).Scan(&usage.AssetBytes).Error
	return usage, err
}

func canvasEpochID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// syncLegacyCanvasWrite projects every legacy save onto the durable command
// stream without importing commands (which depends on repository). Ordinary
// saves emit changed entities/fields; a new epoch carries a complete reset.
func (r *Repository) syncLegacyCanvasWrite(before, after *model.CanvasProject, reset bool) error {
	// Some repository consumers intentionally use the pre-command schema. Once
	// the epoch table exists, failures in any command table must fail the save.
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
	// Legacy events support synchronization, not undo. Snapshot resources have
	// their own bounded retention; only actual command history owns undo pins.
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

func (r *Repository) hasCommandSchema() (bool, error) {
	var count int64
	table := r.db.NamingStrategy.TableName("CanvasDocumentEpoch")
	if r.Dialect() == "postgres" {
		err := r.db.Raw("SELECT CASE WHEN to_regclass(?) IS NULL THEN 0 ELSE 1 END", table).Scan(&count).Error
		return count == 1, err
	}
	err := r.db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error
	return count == 1, err
}
