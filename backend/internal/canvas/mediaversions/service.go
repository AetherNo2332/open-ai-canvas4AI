package mediaversions

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type Scope = repository.MediaVersionScope

type State struct {
	Epoch             string `json:"epoch"`
	Generation        int64  `json:"generation"`
	CurrentVersionID  string `json:"currentVersionId,omitempty"`
	PendingVersionID  string `json:"pendingVersionId,omitempty"`
	ActiveTaskID      string `json:"activeTaskId,omitempty"`
	SelectionRevision int64  `json:"selectionRevision"`
	VersionCount      int64  `json:"versionCount"`
	LastError         string `json:"lastError,omitempty"`
}

type Error struct{ Reason, Message string }

func (e *Error) Error() string          { return e.Message }
func fail(reason, message string) error { return &Error{reason, message} }
func metadata(node map[string]any) map[string]any {
	m, _ := node["metadata"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		node["metadata"] = m
	}
	return m
}
func str(v any) string { s, _ := v.(string); return s }
func ReadState(node map[string]any) State {
	var state State
	if node != nil {
		raw, _ := json.Marshal(metadata(node)["mediaVersion"])
		_ = json.Unmarshal(raw, &state)
	}
	return state
}
func writeState(node map[string]any, state State) {
	raw, _ := json.Marshal(state)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	metadata(node)["mediaVersion"] = value
}
func matches(v *model.CanvasMediaVersion, s Scope) bool {
	return v.OwnerID == s.OwnerID && v.CanvasID == s.CanvasID && v.Epoch == s.Epoch && v.NodeID == s.NodeID && v.Generation == s.Generation
}
func Supported(mode string) bool { return mode == "image" || mode == "video" || mode == "audio" }

// ScopeForNode reads the host's incarnation guard, never a mutable node field.
func ScopeForNode(repo *repository.Repository, owner, canvas, nodeID string) (Scope, error) {
	epoch, err := repo.CommandEpoch(owner, canvas)
	if err != nil {
		return Scope{}, err
	}
	versions, err := repo.CommandVersions(canvas, epoch.Epoch)
	if err != nil {
		return Scope{}, err
	}
	v, ok := versions["node:"+nodeID]
	if !ok || v.Deleted {
		return Scope{}, fail("target_deleted", "目标节点已删除或重建")
	}
	return Scope{OwnerID: owner, CanvasID: canvas, Epoch: epoch.Epoch, NodeID: nodeID, Generation: v.Generation}, nil
}

var mediaKeys = model.CanvasMediaFields

func capture(m map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range mediaKeys {
		if v, ok := m[key]; ok {
			result[key] = v
		}
	}
	raw, _ := json.Marshal(result)
	_ = json.Unmarshal(raw, &result)
	return result
}
func apply(node map[string]any, result map[string]any) {
	m := metadata(node)
	for _, key := range mediaKeys {
		delete(m, key)
	}
	for key, value := range capture(result) {
		m[key] = value
	}
	m["status"] = "success"
	for _, key := range []string{"errorDetails", "generationErrorCode", "resourceReloadAvailable", "failedPromptFingerprint", "richText", "runninghubWorkflowConfig", "workflowProvider", "runningHubWorkflowId", "runningHubWorkflowKind", "workflowParameters", "producedModelCandidate"} {
		delete(m, key)
	}
}
func readyResource(repo *repository.Repository, owner, mode string, m map[string]any) (string, error) {
	key := str(m["storageKey"])
	if !strings.HasPrefix(key, "resource:") {
		return "", fail("result_resource_unavailable", "媒体尚未保存到账号资源库")
	}
	id := strings.TrimPrefix(key, "resource:")
	r, err := repo.ResourceForUser(owner, id)
	if err != nil || r.Status != model.ResourceStatusReady || !strings.HasPrefix(strings.ToLower(r.MimeType), mode+"/") {
		return "", fail("result_resource_unavailable", "媒体资源不可用或类型不匹配")
	}
	return id, nil
}
func encode(value any) string { raw, _ := json.Marshal(value); return string(raw) }

// scrub keeps reproducible configuration without persisting credentials twice.
func scrub(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range v {
			switch strings.ToLower(k) {
			case "apikey", "api_key", "authorization", "cookie", "token", "secret", "password", "headers":
				continue
			}
			out[k] = scrub(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = scrub(item)
		}
		return out
	default:
		return value
	}
}

// Begin joins task admission's transaction. The task and billed reservation
// must roll back together if any version or canvas write fails.
func Begin(repo *repository.Repository, scope Scope, node map[string]any, task model.Task, intent string) (*model.CanvasMediaVersion, error) {
	if scope.OwnerID == "" || scope.Epoch == "" || node == nil || str(node["id"]) != scope.NodeID || task.UserID != scope.OwnerID || task.ID == "" {
		return nil, fail("invalid_version_target", "媒体版本目标或任务身份无效")
	}
	mode := str(node["type"])
	if !Supported(mode) {
		return nil, fail("target_type_mismatch", "目标不是图片、视频或音频节点")
	}
	if metadata(node)["locked"] == true {
		return nil, fail("target_locked", "目标节点已锁定")
	}
	prior, err := repo.CanvasMediaVersionForTask(scope.OwnerID, task.ID)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if !matches(prior, scope) {
			return nil, fail("task_binding_changed", "原任务已关联其他节点版本")
		}
		return prior, nil
	}
	if err := CheckAdmission(repo, scope, node, intent); err != nil {
		return nil, err
	}
	state := ReadState(node)
	if state.Epoch != scope.Epoch || state.Generation != scope.Generation {
		state = State{Epoch: scope.Epoch, Generation: scope.Generation}
	}
	m := metadata(node)
	hasMedia := str(m["storageKey"]) != "" || str(m["content"]) != ""
	sequence, err := repo.CanvasMediaVersionSequence(scope)
	if err != nil {
		return nil, err
	}
	if state.CurrentVersionID == "" && hasMedia {
		resourceID, err := readyResource(repo, scope.OwnerID, mode, m)
		if err != nil {
			return nil, err
		}
		sequence++
		base := &model.CanvasMediaVersion{ID: "mv" + rand.Text(), OwnerID: scope.OwnerID, CanvasID: scope.CanvasID, Epoch: scope.Epoch, NodeID: scope.NodeID, Generation: scope.Generation, Sequence: sequence, SourceTaskID: str(m["taskId"]), Mode: mode, Status: "ready", MediaJSON: encode(capture(m)), ResourceID: resourceID, Adoption: "imported"}
		if err = repo.InsertCanvasMediaVersion(base); err != nil {
			return nil, err
		}
		state.CurrentVersionID = base.ID
	}
	sequence++
	var input any
	_ = json.Unmarshal([]byte(task.InputJSON), &input)
	taskID := task.ID
	row := &model.CanvasMediaVersion{ID: "mv" + rand.Text(), OwnerID: scope.OwnerID, CanvasID: scope.CanvasID, Epoch: scope.Epoch, NodeID: scope.NodeID, Generation: scope.Generation, Sequence: sequence, TaskID: &taskID, ParentVersionID: state.CurrentVersionID, Mode: mode, Status: string(task.Status), SelectionRevision: state.SelectionRevision, RequestJSON: encode(map[string]any{"prompt": task.Prompt, "model": task.Model, "input": scrub(input), "geometry": map[string]any{"position": node["position"], "width": node["width"], "height": node["height"]}}), Adoption: "pending"}
	if err = repo.InsertCanvasMediaVersion(row); err != nil {
		return nil, err
	}
	state.PendingVersionID, state.ActiveTaskID, state.VersionCount, state.LastError = row.ID, task.ID, sequence, ""
	writeState(node, state)
	if state.CurrentVersionID == "" {
		m["status"] = "loading"
		delete(m, "errorDetails")
	}
	return row, nil
}

// CheckAdmission is read-only and runs before preparing a billed task. The
// caller must keep the account/canvas lock until Begin and task commit finish.
func CheckAdmission(repo *repository.Repository, scope Scope, node map[string]any, intent string) error {
	if node == nil || str(node["id"]) != scope.NodeID || !Supported(str(node["type"])) {
		return fail("invalid_version_target", "媒体目标无效")
	}
	if metadata(node)["locked"] == true {
		return fail("target_locked", "目标节点已锁定")
	}
	if intent != "" && intent != "create" && intent != "new_version" {
		return fail("invalid_intent", "生成意图无效")
	}
	state := ReadState(node)
	active, err := repo.PendingCanvasMediaVersion(scope)
	if err != nil {
		return err
	}
	if active != nil || (state.Epoch == scope.Epoch && state.Generation == scope.Generation && state.PendingVersionID != "") {
		return fail("task_in_progress", "已有生成或待写回任务，请先查询原任务")
	}
	m := metadata(node)
	legacy := str(m["taskId"])
	if legacy == "" {
		legacy = str(m["generationTaskId"])
	}
	if legacy != "" {
		original, err := repo.TaskForUser(scope.OwnerID, legacy)
		if err != nil {
			return fail("task_unavailable", "无法核对原任务，请先恢复任务记录")
		}
		switch original.Status {
		case model.TaskStatusQueued, model.TaskStatusRunning:
			return fail("task_in_progress", "原任务仍在执行，请先查询原任务")
		case model.TaskStatusSubmissionUncertain:
			return fail("submission_uncertain", "原任务提交结果未知，请先核对")
		}
		if original.MediaStage != "" && original.Status != model.TaskStatusCancelled && str(m["storageKey"]) == "" {
			return fail("materialization_pending", "原任务已有媒体保存过程，请先恢复原任务")
		}
	}
	hasMedia := str(m["storageKey"]) != "" || str(m["content"]) != ""
	if hasMedia && intent != "new_version" {
		return fail("new_version_required", "目标已有媒体，请明确生成新版本")
	}
	if hasMedia && state.CurrentVersionID == "" {
		if _, err := readyResource(repo, scope.OwnerID, str(node["type"]), m); err != nil {
			return err
		}
	}
	return nil
}

// Finish records durable outputs before adopting them. A user selection wins
// over later completion, including an A→B→A selection with the same pointer.
func Finish(repo *repository.Repository, scope Scope, node map[string]any, task model.Task, result map[string]any, outputs []model.CanvasTaskOutput, blockedReason ...string) (bool, string, error) {
	if task.UserID != scope.OwnerID {
		return false, "", fail("task_owner_mismatch", "任务不属于当前账号")
	}
	row, err := repo.CanvasMediaVersionForTask(scope.OwnerID, task.ID)
	if err != nil {
		return false, "", err
	}
	if row == nil {
		return false, "", fail("version_missing", "原任务没有媒体版本记录")
	}
	if row.Status == "ready" || row.Status == "failed" || row.Status == "cancelled" {
		return row.Adoption == "adopted", row.Adoption, nil
	}
	if task.Status != model.TaskStatusSucceeded && task.Status != model.TaskStatusFailed && task.Status != model.TaskStatusCancelled && task.Status != model.TaskStatusSubmissionUncertain {
		return false, "", fail("task_in_progress", "任务尚未完成")
	}
	if task.Status == model.TaskStatusSubmissionUncertain {
		row.Status, row.Adoption = "submission_uncertain", "pending"
		return false, "submission_uncertain", repo.UpdateCanvasMediaVersion(row)
	}
	row.Status, row.Adoption = string(task.Status), "not_adopted"
	if task.Status == model.TaskStatusSucceeded {
		resourceID, e := readyResource(repo, scope.OwnerID, row.Mode, result)
		if e != nil {
			return false, "", e
		}
		row.Status, row.ResourceID, row.MediaJSON, row.OutputsJSON = "ready", resourceID, encode(capture(result)), encode(outputs)
	} else {
		row.Error = "生成未完成，请查看原任务"
	}
	state := ReadState(node)
	eligible := node != nil && matches(row, scope) && str(node["id"]) == scope.NodeID && str(node["type"]) == row.Mode && state.Epoch == scope.Epoch && state.Generation == scope.Generation && state.PendingVersionID == row.ID && state.ActiveTaskID == task.ID
	adopted := false
	if eligible {
		blocked := len(blockedReason) > 0 && blockedReason[0] != ""
		if task.Status == model.TaskStatusSucceeded && state.SelectionRevision == row.SelectionRevision && !blocked {
			apply(node, result)
			state.CurrentVersionID = row.ID
			state.SelectionRevision++
			row.Adoption = "adopted"
			adopted = true
		} else if task.Status == model.TaskStatusSucceeded {
			row.Adoption = "selection_changed"
			if blocked {
				row.Adoption = blockedReason[0]
			}
		}
		state.PendingVersionID, state.ActiveTaskID, state.LastError = "", "", row.Error
		if blocked {
			row.Adoption = blockedReason[0]
		}
		if state.CurrentVersionID == "" {
			metadata(node)["status"], metadata(node)["errorDetails"] = "error", row.Error
			metadata(node)["taskStatus"] = string(task.Status)
		}
		writeState(node, state)
	} else {
		row.Adoption = "target_changed"
	}
	if err = repo.UpdateCanvasMediaVersion(row); err != nil {
		return false, "", err
	}
	return adopted, row.Adoption, nil
}

// Selection is hydrated inside the host transaction. Applying it is pure, so
// the command planner never queries persistence or observes a different read.
type Selection struct {
	Scope   Scope
	Version model.CanvasMediaVersion
	Media   map[string]any
}

func PrepareSelection(repo *repository.Repository, scope Scope, versionID string) (Selection, error) {
	row, err := repo.CanvasMediaVersion(scope, versionID)
	if err != nil {
		return Selection{}, fail("version_missing", "媒体版本不存在或不属于当前节点")
	}
	if row.Status != "ready" {
		return Selection{}, fail("version_not_ready", "该版本尚不可采用")
	}
	var result map[string]any
	if err = json.Unmarshal([]byte(row.MediaJSON), &result); err != nil {
		return Selection{}, fmt.Errorf("invalid version output: %w", err)
	}
	if _, err = readyResource(repo, scope.OwnerID, row.Mode, result); err != nil {
		return Selection{}, err
	}
	return Selection{Scope: scope, Version: *row, Media: result}, nil
}

func ApplySelection(node map[string]any, selection Selection) error {
	scope, row := selection.Scope, selection.Version
	if node == nil || !matches(&row, scope) || str(node["id"]) != scope.NodeID || str(node["type"]) != row.Mode || metadata(node)["locked"] == true {
		return fail("version_not_ready", "该版本不可采用或节点已锁定")
	}
	state := ReadState(node)
	if state.Epoch != scope.Epoch || state.Generation != scope.Generation {
		return fail("version_scope_changed", "节点版本作用域已变化，请重新读取")
	}
	apply(node, selection.Media)
	state.CurrentVersionID = row.ID
	state.SelectionRevision++
	writeState(node, state)
	return nil
}

func Use(repo *repository.Repository, scope Scope, node map[string]any, versionID string) error {
	selection, err := PrepareSelection(repo, scope, versionID)
	if err != nil {
		return err
	}
	return ApplySelection(node, selection)
}
