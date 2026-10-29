package mediaversions

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Scope 是节点化身守卫（纪元 + 实体 generation），只用于确认目标节点没有被删除重建。
type Scope = repository.MediaVersionScope

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
func str(v any) string           { s, _ := v.(string); return s }
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

// Base 是提交时冻结在任务绑定上的版本上下文；写回时据此判断用户是否在生成期间换过当前版本、
// 输出归属或几何是否变化。
type Base struct {
	NodeID           string         `json:"nodeId"`
	CurrentVersionID string         `json:"currentVersionId,omitempty"`
	HadMedia         bool           `json:"hadMedia,omitempty"`
	OutputOwners     []OutputOwner  `json:"outputOwners"`
	Geometry         map[string]any `json:"geometry,omitempty"`
	// Blocked 由画布写入投影单调写入：输出归属曾改变（包括 A→B→A 恢复）后不再自动采用。
	Blocked string `json:"blocked,omitempty"`
	// 写回后记录结果，供任务回执与 Agent 工具结果读取。
	ResultVersionID string `json:"resultVersionId,omitempty"`
	Adopted         bool   `json:"adopted,omitempty"`
	Settled         bool   `json:"settled,omitempty"`
}

func (b Base) Encode() string { raw, _ := json.Marshal(b); return string(raw) }

// BindingBase 返回任务绑定上的版本上下文；没有上下文的绑定不是媒体版本任务。
func BindingBase(binding *model.CanvasTaskBinding) (Base, bool, error) {
	var base Base
	if binding == nil || binding.MediaVersionJSON == "" {
		return base, false, nil
	}
	if err := json.Unmarshal([]byte(binding.MediaVersionJSON), &base); err != nil {
		return base, false, err
	}
	return base, base.NodeID != "", nil
}

func geometry(node map[string]any) map[string]any {
	raw, _ := json.Marshal(map[string]any{"position": node["position"], "width": node["width"], "height": node["height"]})
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// GeometryUnchanged 报告节点位置尺寸与提交时一致。
func (b Base) GeometryUnchanged(node map[string]any) bool {
	return node != nil && reflect.DeepEqual(b.Geometry, geometry(node))
}

// Begin 在提交事务里冻结版本上下文，并把节点标成生成中。旧媒体保留显示，不创建占位版本。
func Begin(node map[string]any, task model.Task, nodes []map[string]any) (Base, error) {
	if node == nil || task.ID == "" {
		return Base{}, fail("invalid_version_target", "媒体版本目标或任务身份无效")
	}
	if !Supported(str(node["type"])) {
		return Base{}, fail("target_type_mismatch", "目标不是图片、视频或音频节点")
	}
	m := metadata(node)
	if m["locked"] == true {
		return Base{}, fail("target_locked", "目标节点已锁定")
	}
	// 与浏览器 mediaVersionFieldsBeforeReplace 一致：生成开始时先把节点当前媒体登记进历史（不是在途占位），
	// 这样它的来源字段不会被新任务的 taskId 覆盖，写回时也能确认当前版本是否被用户换过。
	ensure(node, "", clock())
	base := Base{NodeID: str(node["id"]), HadMedia: HasMedia(node), OutputOwners: OutputOwners(nodes, str(node["id"])), Geometry: geometry(node)}
	if current := Current(node); current != nil {
		base.CurrentVersionID = str(current["id"])
	}
	// 已有媒体时不标 loading：旧媒体继续显示和被引用，用户也能在生成期间切换版本（loading 会阻断采用）。
	// 浏览器的 adoptMediaVersion 会解除 taskId，写回时据此只把新结果加入历史。
	m["taskId"], m["taskStatus"] = task.ID, string(task.Status)
	if !base.HadMedia {
		m["status"] = "loading"
	}
	for _, key := range []string{"errorDetails", "generationErrorCode", "failedPromptFingerprint", "resourceReloadAvailable"} {
		delete(m, key)
	}
	return base, nil
}

// CheckAdmission is read-only and runs before preparing a billed task. The
// caller must keep the account/canvas lock until Begin and task commit finish.
func CheckAdmission(repo *repository.Repository, scope Scope, node map[string]any, intent string, admittedTaskID ...string) error {
	if node == nil || str(node["id"]) != scope.NodeID || !Supported(str(node["type"])) {
		return fail("invalid_version_target", "媒体目标无效")
	}
	if metadata(node)["locked"] == true {
		return fail("target_locked", "目标节点已锁定")
	}
	if intent != "" && intent != "create" && intent != "new_version" {
		return fail("invalid_intent", "生成意图无效")
	}
	admitted := ""
	if len(admittedTaskID) == 1 {
		admitted = admittedTaskID[0]
	}
	if scope.Epoch != "" {
		bindings, err := repo.ActiveCanvasTaskBindings(scope.OwnerID, scope.CanvasID, scope.Epoch)
		if err != nil {
			return err
		}
		for i := range bindings {
			if base, ok, err := BindingBase(&bindings[i]); err == nil && ok && base.NodeID == scope.NodeID && bindings[i].TaskID != admitted {
				return fail("task_in_progress", "节点已有生成中的版本，请先查询原任务")
			}
		}
	}
	m := metadata(node)
	legacy := str(m["taskId"])
	if legacy == "" {
		legacy = str(m["generationTaskId"])
	}
	if legacy != "" && legacy != admitted {
		original, err := repo.TaskForUser(scope.OwnerID, legacy)
		if err != nil || original.ProjectID != "" && original.ProjectID != scope.CanvasID {
			return fail("task_unavailable", "无法核对原任务，请先恢复任务记录")
		}
		switch original.Status {
		case model.TaskStatusQueued, model.TaskStatusRunning:
			return fail("task_in_progress", "原任务仍在执行，请先查询原任务")
		case model.TaskStatusSubmissionUncertain:
			return fail("submission_uncertain", "原任务提交结果未知，请先核对")
		}
		// Successful delivery retains its encrypted checkpoint for provenance.
		// Only unfinished delivery must resume the original paid task.
		deliveryCompleted := original.Status == model.TaskStatusSucceeded && original.MediaStage == "completed"
		if original.Status != model.TaskStatusCancelled && !deliveryCompleted && (original.MediaRecoveryJSON != "" || original.MediaStage != "" && original.MediaStage != "completed") {
			return fail("materialization_pending", "原任务已有媒体保存过程，请先恢复原任务")
		}
		pending, err := repo.CanvasMediaTaskMaterializationPending(scope.OwnerID, legacy)
		if err != nil {
			return err
		}
		if pending && original.Status != model.TaskStatusCancelled {
			return fail("materialization_pending", "原任务尚待写回，请先恢复原任务")
		}
		if intent != "new_version" {
			return fail("new_version_required", "目标已有已结束的生成申请，请确认后明确生成新版本")
		}
	}
	if (HasMedia(node) || len(List(node)) > 0) && intent != "new_version" {
		return fail("new_version_required", "目标已有媒体或版本历史，请明确生成新版本")
	}
	return nil
}

// Outcome 是一次写回的结果；Adopted=false 时新版本只进入历史。
type Outcome struct {
	Reason    string
	VersionID string
	Adopted   bool
}

func releaseTask(m map[string]any, taskID string) {
	if str(m["taskId"]) != taskID {
		return
	}
	for _, key := range taskMetadataKeys {
		delete(m, key)
	}
	if hasMediaFields(m) {
		m["status"] = "success"
	} else {
		m["status"] = "idle"
	}
}

// Finish 把任务结果写回节点。result 是在节点副本上应用任务输出后的节点（与浏览器
// recordReplacedMediaVersion 的 next 同义）。blocked 非空时只释放生成标记，不写入媒体。
func Finish(node, result map[string]any, base Base, task model.Task, blocked string, now time.Time) (map[string]any, Outcome) {
	if node == nil {
		return nil, Outcome{Reason: "target_deleted"}
	}
	m := metadata(node)
	if str(node["type"]) != str(result["type"]) {
		return node, Outcome{Reason: "target_changed"}
	}
	resultMeta := metadata(result)
	media, generation := splitResult(m, resultMeta)
	if str(m["taskId"]) != task.ID {
		// 生成期间用户切换了版本（采用会解除 taskId）或其他写入解除了生成标记：成功结果只登记进历史，
		// 不改动节点当前媒体。taskId 已指向另一个任务时同样只进历史，但报告绑定已变化。
		reason := "selection_changed"
		if str(m["taskId"]) != "" {
			reason = "binding_changed"
		}
		if blocked == "" && task.Status == model.TaskStatusSucceeded && IsVersionNode(node) && hasMediaFields(resultMeta) {
			appended, id := AppendGenerated(node, media, generation, now)
			return appended, Outcome{Reason: reason, VersionID: id}
		}
		return node, Outcome{Reason: reason}
	}
	if blocked != "" {
		releaseTask(m, task.ID)
		return node, Outcome{Reason: blocked}
	}
	if task.Status != model.TaskStatusSucceeded {
		m["taskStatus"], m["errorDetails"] = string(task.Status), safeFailure(task)
		m["status"] = "error"
		if hasMediaFields(m) {
			// 旧媒体继续可用；失败只记录在任务字段上。
			m["status"] = "success"
		}
		return node, Outcome{Reason: "applied"}
	}
	if identity := mediaIdentity(resultMeta); identity != "" && identity == mediaIdentity(m) {
		// 另一位写入方（例如浏览器任务同步）已经提交同一结果。
		recorded := RecordReplaced(node, result, "generate", now)
		current := Current(recorded)
		return recorded, Outcome{Reason: "applied", Adopted: true, VersionID: str(current["id"])}
	}
	currentID := ""
	if current := Current(node); current != nil {
		currentID = str(current["id"])
	}
	if currentID != base.CurrentVersionID {
		appended, id := AppendGenerated(node, media, generation, now)
		releaseTask(metadata(appended), task.ID)
		return appended, Outcome{Reason: "selection_changed", VersionID: id}
	}
	recorded := RecordGenerated(node, media, generation, now)
	if base.HadMedia || !base.GeometryUnchanged(node) {
		recorded["position"], recorded["width"], recorded["height"] = node["position"], node["width"], node["height"]
	} else {
		recorded["position"], recorded["width"], recorded["height"] = result["position"], result["width"], result["height"]
	}
	return recorded, Outcome{Reason: "applied", Adopted: true, VersionID: str(metadata(recorded)["currentMediaVersionId"])}
}

// splitResult 把应用任务输出后的 metadata 拆成媒体字段与其余字段；被输出清除的字段记为 nil（删除）。
func splitResult(before, after map[string]any) (map[string]any, map[string]any) {
	media, rest := map[string]any{}, map[string]any{}
	isMedia := map[string]bool{}
	for _, key := range MediaKeys {
		isMedia[key] = true
		if value, ok := after[key]; ok {
			media[key] = value
		}
	}
	for key, value := range after {
		if !isMedia[key] {
			rest[key] = value
		}
	}
	for key := range before {
		if _, ok := after[key]; !ok && !isMedia[key] && key != "mediaVersions" && key != "currentMediaVersionId" {
			rest[key] = nil
		}
	}
	delete(rest, "mediaVersions")
	delete(rest, "currentMediaVersionId")
	return media, rest
}

func safeFailure(task model.Task) string {
	if task.Status == model.TaskStatusCancelled {
		return "任务已取消"
	}
	if task.Status == model.TaskStatusSubmissionUncertain {
		return "上游接单状态尚未确认，请核对原任务；已停止自动重提"
	}
	return "生成未完成，请查看原任务；当前版本保持不变"
}

// ReleaseRestored 用于整份恢复与撤销重建：恢复出的文档不能复活已结束或不属于当前化身的生成标记。
func ReleaseRestored(node map[string]any) {
	m, _ := node["metadata"].(map[string]any)
	if m == nil || m["status"] != "loading" || !Supported(str(node["type"])) {
		return
	}
	releaseTask(m, str(m["taskId"]))
}

// Item 是 Agent 读到的版本：上游 CanvasMediaVersion 字段的公开投影，不含存储地址。
type Item struct {
	ID         string         `json:"id"`
	Number     float64        `json:"number"`
	CreatedAt  string         `json:"createdAt"`
	Source     string         `json:"source"`
	Media      map[string]any `json:"media"`
	Generation map[string]any `json:"generation,omitempty"`
	IsCurrent  bool           `json:"isCurrent"`
}

type Generating struct {
	TaskID string `json:"taskId"`
}

type Page struct {
	NodeID                string      `json:"nodeId"`
	CurrentMediaVersionID string      `json:"currentMediaVersionId,omitempty"`
	Items                 []Item      `json:"items"`
	Generating            *Generating `json:"generating,omitempty"`
	CanCreateVersion      bool        `json:"canCreateVersion"`
	BlockReason           string      `json:"blockReason,omitempty"`
	BlockIssue            string      `json:"blockIssue,omitempty"`
}

var publicGenerationKeys = []string{"prompt", "model", "size", "quality", "seconds", "references", "taskId"}

func publicMedia(media map[string]any) map[string]any {
	out := map[string]any{}
	if key := str(media["storageKey"]); strings.HasPrefix(key, "resource:") {
		out["resourceId"] = strings.TrimPrefix(key, "resource:")
	}
	for _, key := range []string{"mimeType", "naturalWidth", "naturalHeight", "durationMs"} {
		if value, ok := media[key]; ok && present(value) {
			out[key] = value
		}
	}
	return out
}

// Read 返回节点全部版本（不超过 Limit 条，不分页）。生成中的任务单独给出，不是版本条目。
func Read(repo *repository.Repository, scope Scope, node map[string]any) (Page, error) {
	out := Page{NodeID: scope.NodeID, Items: []Item{}}
	current := Current(node)
	if current != nil {
		out.CurrentMediaVersionID = str(current["id"])
	}
	versions := List(node)
	sort.SliceStable(versions, func(i, j int) bool { return number(versions[i]["number"]) < number(versions[j]["number"]) })
	for _, version := range versions {
		item := Item{ID: str(version["id"]), Number: number(version["number"]), CreatedAt: str(version["createdAt"]), Source: str(version["source"]), Media: publicMedia(versionMedia(version)), IsCurrent: current != nil && str(current["id"]) == str(version["id"])}
		if generation := versionGeneration(version); generation != nil {
			public := map[string]any{}
			for _, key := range publicGenerationKeys {
				if value, ok := generation[key]; ok && present(value) {
					public[key] = value
				}
			}
			if len(public) > 0 {
				item.Generation = public
			}
		}
		out.Items = append(out.Items, item)
	}
	taskID, err := generatingTask(repo, scope, node)
	if err != nil {
		return out, err
	}
	if taskID != "" {
		out.Generating = &Generating{TaskID: taskID}
	}
	if err := CheckAdmission(repo, scope, node, "new_version"); err == nil {
		out.CanCreateVersion = true
	} else if e, ok := err.(*Error); ok {
		out.BlockReason, out.BlockIssue = e.Reason, e.Message
	} else {
		return out, err
	}
	return out, nil
}

// generatingTask 找节点上在途或待写回的媒体版本任务。依据任务绑定而不是节点 taskId：
// 用户在生成期间切换版本会解除节点上的任务标记，但任务仍在执行。
func generatingTask(repo *repository.Repository, scope Scope, node map[string]any) (string, error) {
	candidates := []string{}
	if scope.Epoch != "" {
		bindings, err := repo.ActiveCanvasTaskBindings(scope.OwnerID, scope.CanvasID, scope.Epoch)
		if err != nil {
			return "", err
		}
		for i := range bindings {
			if base, ok, err := BindingBase(&bindings[i]); err == nil && ok && base.NodeID == scope.NodeID {
				return bindings[i].TaskID, nil
			}
		}
	}
	if taskID := str(metadata(node)["taskId"]); taskID != "" {
		candidates = append(candidates, taskID)
	}
	for _, taskID := range candidates {
		task, err := repo.TaskForUser(scope.OwnerID, taskID)
		if repo.IsMissing(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		switch task.Status {
		case model.TaskStatusQueued, model.TaskStatusRunning, model.TaskStatusSubmissionUncertain:
			return taskID, nil
		case model.TaskStatusSucceeded, model.TaskStatusFailed:
			pending, err := repo.CanvasMediaTaskMaterializationPending(scope.OwnerID, taskID)
			if err != nil {
				return "", err
			}
			if pending {
				return taskID, nil
			}
		}
	}
	return "", nil
}
