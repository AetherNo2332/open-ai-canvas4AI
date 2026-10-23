package app

import (
	"encoding/json"
	"sort"
	"time"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const cloudAgentStoryboardOperationClient = "agent:storyboard"

func cloudAgentStoryboardOperationArguments(call cloudAgentCall) (string, string, error) {
	if call.Function.Name != "canvas_edit_storyboard" {
		return "", "", nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "", "", cloudAgentJSONArgumentError(err)
	}
	value, exists := args["operationId"]
	if !exists {
		return "", "", nil
	}
	id, ok := value.(string)
	if !ok || utf8.RuneCountInString(id) < 8 {
		return "", "", cloudAgentFieldError("operationId", "invalid_value", "operationId 需要8到128字符的稳定批次标识")
	}
	if err := validateCloudAgentID(id, "operationId", 128); err != nil {
		return "", "", err
	}
	// Preconditions may be refreshed after a conflict; the intended mutation
	// must remain byte-for-byte equivalent as canonical JSON values.
	delete(args, "snapshotHash")
	delete(args, "storyboardRevision")
	raw, err := json.Marshal(args)
	return id, string(raw), err
}

func cloudAgentStoryboardOperationKey(repo *repository.Repository, userID, canvasID, id string) (model.CanvasCommandReceipt, error) {
	epoch := ""
	current, err := repo.CommandEpoch(userID, canvasID)
	if err == nil {
		epoch = current.Epoch
	} else if !repo.IsMissing(err) {
		return model.CanvasCommandReceipt{}, err
	}
	return model.CanvasCommandReceipt{OwnerID: userID, ActorID: userID, CanvasID: canvasID, Epoch: epoch, ClientID: cloudAgentStoryboardOperationClient, OperationID: id}, nil
}

func cloudAgentStoryboardReplay(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (any, bool, error) {
	id, canonical, err := cloudAgentStoryboardOperationArguments(call)
	if err != nil || id == "" {
		return nil, false, err
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, false, err
	}
	key, err := cloudAgentStoryboardOperationKey(repo, userID, canvasID, id)
	if err != nil {
		return nil, false, err
	}
	stored, err := repo.CommandReceipt(key)
	if repo.IsMissing(err) {
		prior, priorErr := repo.CommandReceiptAnyEpoch(key)
		if priorErr == nil && prior.Epoch != key.Epoch {
			return nil, false, cloudAgentFieldError("operationId", "operation_epoch_conflict", "该批次属于已恢复或退役的画布世代，本次未写入；请重新确认当前目标，不得自动重发旧批次")
		}
		if priorErr != nil && !repo.IsMissing(priorErr) {
			return nil, false, priorErr
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if stored.CanonicalJSON != canonical {
		return nil, false, cloudAgentFieldError("operationId", "operation_id_conflict", "该批次标识已有不同参数；本次未写入，请核对原批次或使用新标识")
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stored.ReceiptJSON), &result); err != nil {
		return nil, false, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, false, err
	}
	result["replayed"], result["currentSnapshotHash"] = true, cloudAgentCanvasHash(doc)
	return result, true, nil
}

func cloudAgentStoreStoryboardOperation(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, receipt map[string]any) error {
	id, canonical, err := cloudAgentStoryboardOperationArguments(call)
	if err != nil || id == "" {
		return err
	}
	var args cloudAgentStoryboardEditArgs
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return err
	}
	ids, fields := append([]string{}, args.RowIDs...), map[string]bool{}
	if args.RowID != "" {
		ids = append(ids, args.RowID)
	}
	for _, update := range args.Updates {
		ids = append(ids, update.RowID)
		for field := range update.Patch {
			fields[field] = true
		}
	}
	for field := range args.Patch {
		fields[field] = true
	}
	for _, created := range creationMaps(receipt["createdRows"]) {
		ids = append(ids, stringValue(created["rowId"]))
	}
	if cloudAgentStoryboardAssetAction(args.Action) {
		fields["assets"] = true
	}
	if cloudAgentStoryboardSharedAction(args.Action) {
		fields["references"] = true
	}
	names := []string{}
	for field := range fields {
		names = append(names, field)
	}
	sort.Strings(names)
	receipt["operationId"], receipt["action"] = id, args.Action
	receipt["targets"] = map[string]any{"rowIds": ids, "fields": names}
	key, err := cloudAgentStoryboardOperationKey(repo, userID, canvasID, id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	key.CanonicalJSON, key.ReceiptJSON, key.CreatedAt = canonical, string(raw), time.Now().UTC()
	return repo.InsertCommandReceipt(&key)
}

func cloudAgentStoryboardOperationStatuses(repo *repository.Repository, userID, canvasID, nodeID string, ids []string) ([]any, error) {
	if err := validateCloudAgentStoryboardOperationIDs(ids); err != nil {
		return nil, err
	}
	statuses := []any{}
	for _, id := range ids {
		key, err := cloudAgentStoryboardOperationKey(repo, userID, canvasID, id)
		if err != nil {
			return nil, err
		}
		stored, err := repo.CommandReceipt(key)
		if repo.IsMissing(err) {
			status := "not_found"
			prior, priorErr := repo.CommandReceiptAnyEpoch(key)
			if priorErr == nil && prior.Epoch != key.Epoch {
				status = "retired_epoch"
			} else if priorErr != nil && !repo.IsMissing(priorErr) {
				return nil, priorErr
			}
			statuses = append(statuses, map[string]any{"operationId": id, "status": status})
			continue
		}
		if err != nil {
			return nil, err
		}
		var receipt map[string]any
		if err := json.Unmarshal([]byte(stored.ReceiptJSON), &receipt); err != nil {
			return nil, err
		}
		if receipt["nodeId"] != nodeID {
			return nil, cloudAgentFieldError("operationIds", "operation_target_conflict", "批次属于另一张分镜表，请核对目标")
		}
		statuses = append(statuses, map[string]any{"operationId": id, "status": "committed", "action": receipt["action"], "targets": receipt["targets"], "createdRows": receipt["createdRows"], "changed": receipt["changed"]})
	}
	return statuses, nil
}

func validateCloudAgentStoryboardOperationIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > 20 {
		return cloudAgentFieldError("operationIds", "invalid_value", "每次查询1到20个分镜批次")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if utf8.RuneCountInString(id) < 8 || seen[id] {
			return cloudAgentFieldError("operationIds", "invalid_value", "operationIds 含有过短或重复标识")
		}
		if err := validateCloudAgentID(id, "operationId", 128); err != nil {
			return err
		}
		seen[id] = true
	}
	return nil
}
