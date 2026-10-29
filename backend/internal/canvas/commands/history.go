package commands

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type HistoryState struct {
	CanUndo bool `json:"canUndo"`
	CanRedo bool `json:"canRedo"`
}

func (s *Service) ReadHistoryState(ctx context.Context, p identity.Principal, canvasID, epochID, clientID string) (HistoryState, error) {
	if err := validateClient(p, clientID, "history-state"); err != nil {
		return HistoryState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out HistoryState
	err := s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, canvasID, "canvas.read"); err != nil {
			return err
		}
		_, epoch, _, _, err := s.state(tx, p, canvasID)
		if err != nil {
			return err
		}
		if epochID != epoch.Epoch {
			return failure("document_epoch_mismatch", "文档世代已变化")
		}
		for _, state := range []string{"applied", "undone"} {
			_, err := tx.ActorHistory(p.OwnerID, canvasID, epochID, p.ID, clientID, state)
			if err != nil && !tx.IsMissing(err) {
				return err
			}
			if err == nil {
				if state == "applied" {
					out.CanUndo = true
				} else {
					out.CanRedo = true
				}
			}
		}
		return nil
	})
	return out, err
}

func (s *Service) Undo(ctx context.Context, p identity.Principal, canvasID, epoch, clientID, operationID string) (Receipt, error) {
	return s.changeHistory(ctx, p, canvasID, epoch, clientID, operationID, false)
}
func (s *Service) Redo(ctx context.Context, p identity.Principal, canvasID, epoch, clientID, operationID string) (Receipt, error) {
	return s.changeHistory(ctx, p, canvasID, epoch, clientID, operationID, true)
}
func (s *Service) changeHistory(ctx context.Context, p identity.Principal, canvasID, epochID, clientID, operationID string, redo bool) (Receipt, error) {
	if err := validateClient(p, clientID, operationID); err != nil {
		return Receipt{}, err
	}
	action, state, nextState := "undo", "applied", "undone"
	if redo {
		action, state, nextState = "redo", "undone", "applied"
	}
	req := Command{Protocol: ProtocolVersion, CanvasID: canvasID, DocumentEpoch: epochID, ClientID: clientID, OperationID: operationID}
	body, _ := canonical(map[string]any{"action": action, "canvasId": canvasID, "documentEpoch": epochID, "clientId": clientID, "operationId": operationID})
	s.mu.Lock()
	var out Receipt
	fresh := false
	err := s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, canvasID, "canvas.write"); err != nil {
			return err
		}
		row, epoch, versions, before, err := s.state(tx, p, canvasID)
		if err != nil {
			return err
		}
		if epochID != epoch.Epoch {
			return failure("document_epoch_mismatch", "文档世代已变化")
		}
		previous, err := tx.CommandReceipt(receiptKey(p, req))
		if err == nil {
			if previous.CanonicalJSON != body {
				return failure("operation_id_reused", "操作 ID 已用于不同请求")
			}
			return json.Unmarshal([]byte(previous.ReceiptJSON), &out)
		}
		if !tx.IsMissing(err) {
			return err
		}
		history, err := tx.ActorHistory(p.OwnerID, canvasID, epochID, p.ID, clientID, state)
		if tx.IsMissing(err) {
			return failure("history_empty", "没有可撤销或重做的操作")
		}
		if err != nil {
			return err
		}
		expected := map[string]int64{}
		if err = json.Unmarshal([]byte(history.ExpectedVersionsJSON), &expected); err != nil {
			return err
		}
		for key, version := range expected {
			if versions[key].Version != version {
				return canvasFieldError(key, "history_conflict", "目标已被后续操作修改，本次历史操作未写入")
			}
		}
		var changes ChangeSet
		source := history.InverseJSON
		if redo {
			source = history.ForwardJSON
		}
		if err = json.Unmarshal([]byte(source), &changes); err != nil {
			return err
		}
		after, err := ApplyChanges(before, changes)
		if err != nil {
			return err
		}
		mediaOps, err := reconcileHistoryMedia(tx, p.OwnerID, canvasID, epochID, row.Revision+1, before, after, changes.RecreatedEntities)
		if err != nil {
			return err
		}
		if err = validateManagedMedia(before, after, mediaOps); err != nil {
			return err
		}
		recreated := changes.RecreatedEntities
		changes = Diff(before, after)
		changes.RecreatedEntities = recreated
		if err = validateResult(after); err != nil {
			return err
		}
		v, err := s.validation(ctx, tx, p, after, nil)
		if err != nil {
			return err
		}
		resources := resourceIDs(after)
		for _, id := range resources {
			if !v.ResourceAllowed(id) {
				return failure("resource_not_owned", "历史资源不可用")
			}
		}
		writes := map[string]bool{}
		for key := range expected {
			writes[key] = true
		}
		plan := PlanResult{Document: after, Changes: changes, Inverse: Diff(after, before), WriteSet: writes, ResourceIDs: resources}
		out, err = s.persist(tx, p, req, body, row, epoch, before, plan, false)
		if err != nil {
			return err
		}
		for key := range expected {
			expected[key] = out.Revision
		}
		history.ExpectedVersionsJSON, err = canonical(expected)
		if err != nil {
			return err
		}
		history.State = nextState
		if err = tx.SaveActorHistory(history); err != nil {
			return err
		}
		// Only our own successful undo/redo may advance a neighboring history
		// precondition, and only when its required value has actually been restored.
		for _, candidateState := range []string{"applied", "undone"} {
			candidate, err := tx.ActorHistory(p.OwnerID, canvasID, epochID, p.ID, clientID, candidateState)
			if tx.IsMissing(err) {
				continue
			}
			if err != nil {
				return err
			}
			if candidate.ID == history.ID {
				continue
			}
			targetJSON := candidate.ForwardJSON
			if candidateState == "undone" {
				targetJSON = candidate.InverseJSON
			}
			var target ChangeSet
			if err = json.Unmarshal([]byte(targetJSON), &target); err != nil {
				return err
			}
			candidateExpected := map[string]int64{}
			if err = json.Unmarshal([]byte(candidate.ExpectedVersionsJSON), &candidateExpected); err != nil {
				return err
			}
			modified := false
			for key := range writes {
				if _, ok := candidateExpected[key]; ok && changeSetMatchesEntity(after, target, key) {
					candidateExpected[key] = out.Revision
					modified = true
				}
			}
			if modified {
				candidate.ExpectedVersionsJSON, err = canonical(candidateExpected)
				if err != nil {
					return err
				}
				if err = tx.SaveActorHistory(candidate); err != nil {
					return err
				}
			}
		}
		fresh = true
		return nil
	})
	s.mu.Unlock()
	if err == nil && fresh && s.options.AfterCommit != nil {
		s.options.AfterCommit(out)
	}
	return out, err
}

func changeSetMatchesEntity(doc Document, changes ChangeSet, key string) bool {
	if strings.HasPrefix(key, "drawing:") {
		key = "node:" + strings.TrimPrefix(key, "drawing:")
	}
	for _, change := range changes.Entities {
		if change.Key != key {
			continue
		}
		actual := entity(doc, key)
		if change.Delete {
			return actual == nil
		}
		left, _ := canonical(historyEntityValue(actual, change.Value))
		right, _ := canonical(historyEntityValue(change.Value, actual))
		return left == right
	}
	if strings.HasPrefix(key, "project:") {
		field := strings.TrimPrefix(key, "project:")
		if field == "nodeOrder" || field == "connectionOrder" {
			name := "nodes"
			expected := changes.NodeOrder
			if field == "connectionOrder" {
				name = "connections"
				expected = changes.ConnectionOrder
			}
			if expected == nil {
				return false
			}
			actual := []string{}
			for _, node := range maps(doc[name]) {
				actual = append(actual, text(node["id"]))
			}
			left, _ := canonical(actual)
			right, _ := canonical(expected)
			return left == right
		}
		if change, ok := changes.Fields[field]; ok {
			value, exists := doc[field]
			if change.Delete {
				return !exists
			}
			left, _ := canonical(value)
			right, _ := canonical(change.Value)
			return exists && left == right
		}
	}
	return false
}
func (s *Service) RestoreVersion(ctx context.Context, p identity.Principal, canvasID, epochID, clientID, operationID string, expectedRevision int64, document Document) (Snapshot, error) {
	if p.Role != "owner" || p.ID != p.OwnerID {
		return Snapshot{}, failure("permission_denied", "仅主机所有者可恢复版本")
	}
	if err := validateClient(p, clientID, operationID); err != nil {
		return Snapshot{}, err
	}
	body, err := canonical(map[string]any{"action": "restore", "canvasId": canvasID, "documentEpoch": epochID, "clientId": clientID, "operationId": operationID, "expectedRevision": expectedRevision, "document": document})
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	var out Snapshot
	var emitted Receipt
	fresh := false
	err = s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, canvasID, "canvas.write"); err != nil {
			return err
		}
		req := Command{Protocol: ProtocolVersion, CanvasID: canvasID, DocumentEpoch: epochID, ClientID: clientID, OperationID: operationID}
		previous, err := tx.CommandReceipt(receiptKey(p, req))
		if err == nil {
			if previous.CanonicalJSON != body {
				return failure("operation_id_reused", "操作 ID 已用于不同请求")
			}
			var receipt Receipt
			if err = json.Unmarshal([]byte(previous.ReceiptJSON), &receipt); err != nil {
				return err
			}
			if receipt.Reset == nil {
				return failure("invalid_receipt", "恢复回执不可读取")
			}
			out = *receipt.Reset
			return nil
		}
		if !tx.IsMissing(err) {
			return err
		}
		row, epoch, _, before, err := s.state(tx, p, canvasID, true)
		if err != nil {
			return err
		}
		if epoch.Epoch != epochID {
			return failure("document_epoch_mismatch", "文档世代已变化")
		}
		if expectedRevision != row.Revision {
			return failure("revision_conflict", "画布已有新的编辑，请核对最新内容后再恢复")
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return err
		}
		var after Document
		if err = json.Unmarshal(raw, &after); err != nil {
			return err
		}
		if after == nil {
			return BadAuthRequest("恢复文档无效")
		}
		after["id"] = canvasID
		after["projectId"] = row.ProjectID
		after["createdAt"] = row.CreatedAt.Format(time.RFC3339Nano)
		delete(after, "_deleted")
		if err = validateResult(after); err != nil {
			return err
		}
		v, err := s.validation(ctx, tx, p, after, nil)
		if err != nil {
			return err
		}
		resources := resourceIDs(after)
		for _, id := range resources {
			if !v.ResourceAllowed(id) {
				return failure("resource_not_owned", "恢复资源不可用")
			}
		}
		writes := map[string]bool{}
		for _, n := range maps(after["nodes"]) {
			writes["node:"+text(n["id"])] = true
			if n["type"] == "drawing" {
				writes["drawing:"+text(n["id"])] = true
			}
		}
		for _, edge := range maps(after["connections"]) {
			writes["connection:"+text(edge["id"])] = true
		}
		for field := range after {
			if field != "nodes" && field != "connections" {
				writes["project:"+field] = true
			}
		}
		// Retain a pre-restore version and its resource references in the old epoch.
		backup, err := canonical(Diff(Document{"nodes": []any{}, "connections": []any{}}, before))
		if err != nil {
			return err
		}
		if err = tx.SaveActorHistory(&model.CanvasActorHistory{ID: newID(), CanvasID: canvasID, Epoch: epochID, OwnerID: p.OwnerID, ActorID: p.ID, ClientID: clientID, OperationID: operationID, Sequence: epoch.EventSeq + 1, State: "archived", ForwardJSON: backup, InverseJSON: "{}", ExpectedVersionsJSON: "{}", CreatedAt: time.Now()}); err != nil {
			return err
		}
		if err = tx.SaveCommandResources(p.OwnerID, canvasID, epochID, operationID, resourceIDs(before), resourceIDs(before)); err != nil {
			return err
		}
		epoch.Epoch = newID()
		for _, node := range maps(after["nodes"]) {
			// 新纪元不继承旧任务的生成标记；版本历史随文档一起恢复。
			mediaversions.ReleaseRestored(node)
		}
		plan := PlanResult{Document: after, Changes: Diff(before, after), Inverse: Diff(after, before), WriteSet: writes, ResourceIDs: resources}
		emitted, err = s.persist(tx, p, req, body, row, epoch, before, plan, false, "before_restore")
		if err != nil {
			return err
		}
		versions, err := tx.CommandVersions(canvasID, epoch.Epoch)
		if err != nil {
			return err
		}
		out = snapshot(row, epoch, versions, after)
		emitted.Reset = &out
		encoded, err := canonical(emitted)
		if err != nil {
			return err
		}
		if err = tx.UpdateCommandReceiptEvent(receiptKey(p, req), encoded, epoch.EventSeq); err != nil {
			return err
		}
		fresh = true
		return nil
	})
	s.mu.Unlock()
	if err == nil && fresh && s.options.AfterCommit != nil {
		s.options.AfterCommit(emitted)
	}
	return out, err
}
