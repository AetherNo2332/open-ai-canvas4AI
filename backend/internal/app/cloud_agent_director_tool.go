package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// director 是 Agent 操作导演台（浏览器预演工作台）的唯一工具。四个 action 共用一份
// 参数合同：read 只读；create_scene / apply_patch 复用受控预演场景读写与哈希校验；
// preview 请求浏览器视口录制白模视频，并以可恢复的外部等待挂起本次调用。
// 写入与 preview 执行前发 director_open 运行事件，前端据此自动拉起导演台。

const (
	cloudAgentDirectorTool             = "director"
	cloudAgentDirectorPreviewDefault   = 120 * time.Second
	cloudAgentDirectorPreviewMax       = 30 * time.Minute
	cloudAgentDirectorWorkstationW     = 520.0
	cloudAgentDirectorWorkstationH     = 340.0
	cloudAgentDirectorViewportReason   = "director_viewport_unavailable"
	cloudAgentDirectorPreviewNotFound  = kernel.ErrorReason("director_preview_not_found")
	cloudAgentDirectorPreviewErrorMax  = 500
	cloudAgentDirectorPreviewWaitKind  = "director_preview"
	cloudAgentDirectorPreviewWaitLabel = "等待导演台浏览器视口录制预演"
)

var cloudAgentDirectorActions = []string{"read", "create_scene", "apply_patch", "preview"}

type cloudAgentDirectorArgs struct {
	Action            string          `json:"action"`
	NodeID            string          `json:"nodeId"`
	SceneID           string          `json:"sceneId"`
	ShotID            string          `json:"shotId"`
	Template          string          `json:"template"`
	Title             string          `json:"title"`
	Patch             json.RawMessage `json:"patch"`
	ExpectedSceneHash string          `json:"expectedSceneHash"`
}

// cloudAgentDirectorPreview 是 preview 调用挂起时的持久等待：requestId 交给前端，
// 截止时间写进检查点，超时由下一次工具推进统一回执，不在进程内睡眠。
type cloudAgentDirectorPreview struct {
	RequestID string    `json:"requestId"`
	CallID    string    `json:"callId"`
	CanvasID  string    `json:"canvasId"`
	NodeID    string    `json:"nodeId,omitempty"`
	SceneID   string    `json:"sceneId"`
	ShotID    string    `json:"shotId"`
	Duration  float64   `json:"duration"`
	FPS       int       `json:"fps"`
	Deadline  time.Time `json:"deadline"`
}

func cloudAgentDirectorPreviewTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CANVAS_AGENT_DIRECTOR_PREVIEW_TIMEOUT"))
	if raw == "" {
		return cloudAgentDirectorPreviewDefault
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		var seconds int
		if _, scanErr := fmt.Sscanf(raw, "%d", &seconds); scanErr != nil || fmt.Sprint(seconds) != raw {
			return cloudAgentDirectorPreviewDefault
		}
		value = time.Duration(seconds) * time.Second
	}
	if value <= 0 || value > cloudAgentDirectorPreviewMax {
		return cloudAgentDirectorPreviewDefault
	}
	return value
}

func cloudAgentDirectorDecode(call cloudAgentCall) (cloudAgentDirectorArgs, error) {
	var args cloudAgentDirectorArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return args, cloudAgentJSONArgumentError(err)
	}
	switch args.Action {
	case "read", "create_scene", "apply_patch", "preview":
	default:
		return args, cloudAgentFieldError("action", "invalid_value", "director.action 只能是 read、create_scene、apply_patch、preview 之一")
	}
	for _, field := range []struct{ name, value, label string }{{"nodeId", args.NodeID, "导演台节点 ID"}, {"sceneId", args.SceneID, "导演场景 ID"}, {"shotId", args.ShotID, "导演镜头 ID"}} {
		if field.value != "" {
			if err := validateCloudAgentID(field.value, field.label, 80); err != nil {
				return args, cloudAgentFieldError(field.name, "invalid_value", cloudAgentSafeToolError(err))
			}
		}
	}
	return args, nil
}

// cloudAgentWriteCall 按 action 判定 director 是否为写：read 只读，其余为写。
// 参数无法解析时按写处理（fail closed），审批与只读拦截都不会因此放松。
func cloudAgentWriteCall(call cloudAgentCall) bool {
	if call.Function.Name != cloudAgentDirectorTool {
		return cloudAgentWrite(call.Function.Name)
	}
	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return true
	}
	return args.Action != "read"
}

func cloudAgentDirectorToolSchema(readOnly bool) map[string]any {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "maxLength": 80, "description": description}
	}
	actions := cloudAgentDirectorActions
	if readOnly {
		actions = []string{"read"}
	}
	properties := map[string]any{
		"action":            map[string]any{"type": "string", "enum": actions, "description": "read 只读；其余为写，按权限审批"},
		"nodeId":            str("可选：已绑定场景的导演台节点 ID，可代替 sceneId"),
		"sceneId":           str("场景 ID；create_scene 省略时由系统分配"),
		"shotId":            str("可选镜头 ID；preview 默认活动镜头"),
		"expectedSceneHash": map[string]any{"type": "string", "maxLength": 128, "description": "read 返回的场景 snapshotHash；apply_patch 必填"},
	}
	if !readOnly {
		properties["template"] = map[string]any{"type": "string", "enum": []string{"empty", "monologue", "dialogue", "blocking", "product", "action_chase", "interior", "crowd"}, "description": "create_scene 场景模板"}
		properties["title"] = map[string]any{"type": "string", "maxLength": cloudAgentPrevisTextLimit, "description": "create_scene 场景与节点标题"}
		properties["patch"] = map[string]any{
			"type": "object", "description": "apply_patch 补丁，只含 operations（1–32 项受限语义操作）",
			"properties":           map[string]any{"operations": map[string]any{"type": "array", "minItems": 1, "maxItems": cloudAgentPrevisMaxOperations, "items": cloudAgentPrevisPatchOperationSchema()}},
			"required":             []string{"operations"},
			"additionalProperties": false,
		}
	}
	return properties
}

func cloudAgentDirectorContracts(schema map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	if _, writable := properties["patch"]; !writable {
		return
	}
	schema["oneOf"] = []map[string]any{
		{"properties": map[string]any{"action": map[string]any{"const": "read"}}},
		{"properties": map[string]any{"action": map[string]any{"const": "create_scene"}}, "required": []string{"template"}},
		{"properties": map[string]any{"action": map[string]any{"const": "apply_patch"}}, "required": []string{"sceneId", "patch", "expectedSceneHash"}},
		{"properties": map[string]any{"action": map[string]any{"const": "preview"}}},
	}
}

// cloudAgentDirectorResolveScene 从 nodeId 或 sceneId 定位场景与工作站节点。
func cloudAgentDirectorResolveScene(doc map[string]any, args cloudAgentDirectorArgs) (map[string]any, string, error) {
	scenes := creationMaps(doc["previsScenes"])
	sceneID := args.SceneID
	nodeID := args.NodeID
	if nodeID != "" {
		var node map[string]any
		for _, candidate := range creationMaps(doc["nodes"]) {
			if stringValue(candidate["id"]) == nodeID {
				node = candidate
				break
			}
		}
		if node == nil {
			return nil, "", BadAuthRequest("导演台节点不存在或不属于当前画布")
		}
		metadata, _ := node["metadata"].(map[string]any)
		bound := stringValue(metadata["previsSceneId"])
		if bound == "" {
			return nil, "", BadAuthRequest("该节点没有绑定导演场景")
		}
		if sceneID != "" && sceneID != bound {
			return nil, "", cloudAgentFieldError("sceneId", "invalid_value", "sceneId 与 nodeId 绑定的场景不一致")
		}
		sceneID = bound
	}
	if sceneID == "" {
		return nil, "", nil
	}
	scene, ok := findPrevisScene(scenes, sceneID)
	if !ok {
		return nil, "", BadAuthRequest("导演场景不存在或不属于当前画布")
	}
	if nodeID == "" {
		nodeID = cloudAgentDirectorBoundNode(doc, sceneID)
	}
	return scene, nodeID, nil
}

func cloudAgentDirectorBoundNode(doc map[string]any, sceneID string) string {
	for _, node := range creationMaps(doc["nodes"]) {
		metadata, _ := node["metadata"].(map[string]any)
		if stringValue(metadata["previsSceneId"]) == sceneID {
			return stringValue(node["id"])
		}
	}
	return ""
}

// cloudAgentDirectorRead 返回场景目录或单个场景的安全投影，附带工作站节点 ID。
func cloudAgentDirectorRead(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (any, error) {
	args, err := cloudAgentDirectorDecode(call)
	if err != nil {
		return nil, err
	}
	_, doc, err := cloudAgentPrevisCanvas(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	scene, nodeID, err := cloudAgentDirectorResolveScene(doc, args)
	if err != nil {
		return nil, err
	}
	read := cloudAgentCall{ID: call.ID}
	read.Function.Name = cloudAgentDirectorTool
	payload := map[string]any{"includeTransforms": true}
	if scene != nil {
		payload["sceneId"] = stringValue(scene["id"])
		if args.ShotID != "" {
			payload["shotId"] = args.ShotID
		}
	}
	encoded, _ := json.Marshal(payload)
	read.Function.Arguments = string(encoded)
	result, err := cloudAgentPrevisSceneRead(repo, userID, canvasID, read)
	if err != nil {
		return nil, err
	}
	fields, _ := result.(map[string]any)
	if fields != nil {
		if nodeID != "" {
			fields["nodeId"] = nodeID
		}
		fields["guidance"] = "安全摘要。修改用 director apply_patch 并把 snapshotHash 作为 expectedSceneHash；预演用 director preview。snapshotHash 只代表该场景。"
	}
	return result, nil
}

type cloudAgentDirectorPlan struct {
	Mutation *cloudAgentPrevisMutationPlan
	Args     cloudAgentDirectorArgs
	NodeID   string
	SceneID  string
	ShotID   string
}

// prepareCloudAgentDirectorMutation 把 director 写调用编译成现有的受控预演修改计划；
// 审批预览与真实执行使用同一份计划，结果写入 project.previsScenes。
func prepareCloudAgentDirectorMutation(repo *repository.Repository, userID, canvasID, runID string, call cloudAgentCall) (*cloudAgentDirectorPlan, error) {
	args, err := cloudAgentDirectorDecode(call)
	if err != nil {
		return nil, err
	}
	switch args.Action {
	case "create_scene":
		return prepareCloudAgentDirectorCreate(repo, userID, canvasID, runID, call, args)
	case "apply_patch":
		return prepareCloudAgentDirectorPatch(repo, userID, canvasID, call, args)
	default:
		return nil, BadAuthRequest("director 写入只支持 create_scene 或 apply_patch")
	}
}

func prepareCloudAgentDirectorCreate(repo *repository.Repository, userID, canvasID, runID string, call cloudAgentCall, args cloudAgentDirectorArgs) (*cloudAgentDirectorPlan, error) {
	if args.Template == "" {
		return nil, cloudAgentFieldError("template", "required", "create_scene 必须指定 template")
	}
	if len(args.Patch) > 0 || args.ExpectedSceneHash != "" {
		return nil, cloudAgentFieldError("patch", "unexpected_field", "create_scene 不接受 patch 或 expectedSceneHash")
	}
	sceneID := args.SceneID
	if sceneID == "" {
		sceneID = "scene-" + cloudAgentID(userID, runID+":director:"+call.ID)[2:18]
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = "导演场景"
	}
	canvas, doc, err := cloudAgentPrevisCanvas(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	createArgs, _ := json.Marshal(cloudAgentPrevisSceneCreateArgs{CanvasSnapshotHash: cloudAgentCanvasHash(doc), SceneID: sceneID, Title: title, TemplateID: args.Template})
	inner := cloudAgentCall{ID: call.ID}
	inner.Function.Name, inner.Function.Arguments = cloudAgentDirectorTool, string(createArgs)
	plan, err := prepareCloudAgentPrevisSceneCreate(repo, userID, canvasID, inner)
	if err != nil {
		return nil, err
	}
	if plan.Canvas.Revision != canvas.Revision {
		return nil, creationConflict("画布已变化，本次未创建导演场景；请重新读取")
	}
	scene, _ := findPrevisScene(creationMaps(plan.Document["previsScenes"]), sceneID)
	nodeID, shotID, err := cloudAgentDirectorEnsureWorkstation(plan.Document, scene, args.NodeID, title)
	if err != nil {
		return nil, err
	}
	plan.AfterSnapshotHash = cloudAgentCanvasHash(plan.Document)
	plan.Operation = "director_create_scene"
	return &cloudAgentDirectorPlan{Mutation: plan, Args: args, NodeID: nodeID, SceneID: sceneID, ShotID: shotID}, nil
}

func prepareCloudAgentDirectorPatch(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, args cloudAgentDirectorArgs) (*cloudAgentDirectorPlan, error) {
	if args.Template != "" || args.Title != "" {
		return nil, cloudAgentFieldError("template", "unexpected_field", "apply_patch 不接受 template 或 title")
	}
	if strings.TrimSpace(args.ExpectedSceneHash) == "" {
		return nil, cloudAgentFieldError("expectedSceneHash", "required", "apply_patch 必须带 director read 返回的场景 snapshotHash")
	}
	if len(args.Patch) == 0 {
		return nil, cloudAgentFieldError("patch", "required", "apply_patch 必须提供 patch.operations")
	}
	var patch struct {
		Operations json.RawMessage `json:"operations"`
	}
	if err := decodeCloudAgentJSONObject(string(args.Patch), &patch); err != nil {
		return nil, cloudAgentFieldError("patch", "invalid_value", "patch 只能包含 operations 数组")
	}
	_, doc, err := cloudAgentPrevisCanvas(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	scene, nodeID, err := cloudAgentDirectorResolveScene(doc, args)
	if err != nil {
		return nil, err
	}
	if scene == nil {
		return nil, cloudAgentFieldError("sceneId", "required", "apply_patch 必须指定 sceneId 或已绑定场景的 nodeId")
	}
	sceneID := stringValue(scene["id"])
	encoded, _ := json.Marshal(map[string]any{"snapshotHash": args.ExpectedSceneHash, "sceneId": sceneID, "operations": patch.Operations})
	inner := cloudAgentCall{ID: call.ID}
	inner.Function.Name, inner.Function.Arguments = cloudAgentDirectorTool, string(encoded)
	plan, err := prepareCloudAgentPrevisApplyPatch(repo, userID, canvasID, inner)
	if err != nil {
		var fieldErr *cloudAgentFieldArgumentError
		if errors.As(err, &fieldErr) && fieldErr.Field == "snapshotHash" {
			fieldErr.Field = "expectedSceneHash"
		}
		return nil, err
	}
	plan.Operation = "director_apply_patch"
	updated, _ := findPrevisScene(creationMaps(plan.Document["previsScenes"]), sceneID)
	shotID := args.ShotID
	if shotID == "" {
		shotID = stringValue(updated["activeShotId"])
	}
	return &cloudAgentDirectorPlan{Mutation: plan, Args: args, NodeID: nodeID, SceneID: sceneID, ShotID: shotID}, nil
}

// cloudAgentDirectorEnsureWorkstation 保证新场景有一个可由浏览器打开的视频工作站节点
// （workflowKind=shot），与上游 createPrevisShot 的节点形态一致。
func cloudAgentDirectorEnsureWorkstation(doc map[string]any, scene map[string]any, requestedNodeID, title string) (string, string, error) {
	sceneID := stringValue(scene["id"])
	shotID := stringValue(scene["activeShotId"])
	nodes := creationMaps(doc["nodes"])
	if existing := cloudAgentDirectorBoundNode(doc, sceneID); existing != "" {
		return existing, shotID, nil
	}
	nodeID := requestedNodeID
	if nodeID == "" {
		nodeID = "previs-" + sceneID
		if utf8.RuneCountInString(nodeID) > 80 {
			nodeID = "previs-" + creationHash(map[string]any{"sceneId": sceneID})[:40]
		}
	}
	for _, node := range nodes {
		if stringValue(node["id"]) == nodeID {
			return "", "", BadAuthRequest("导演台节点 ID 已被占用")
		}
	}
	shotIndex := 1
	for _, node := range nodes {
		metadata, _ := node["metadata"].(map[string]any)
		if stringValue(metadata["workflowKind"]) == "shot" {
			shotIndex = int(math.Max(float64(shotIndex), numberValue(metadata["shotIndex"], 0)+1))
		}
	}
	x, y := cloudAgentDirectorWorkstationPosition(nodes)
	width, height := cloudAgentDirectorWorkstationW, cloudAgentDirectorWorkstationH
	op := CreationCanvasOp{Type: "add_node", ID: nodeID, NodeType: "video", Title: title, X: &x, Y: &y, Width: &width, Height: &height, Metadata: map[string]any{
		"content": "", "status": "idle", "workflowKind": "shot", "workflowTitle": title, "shotIndex": float64(shotIndex),
		"generationMode": "video", "videoEditOperation": "text_to_video", "composerContent": "",
		"previsSceneId": sceneID, "previsShotId": shotID,
	}}
	doc["nodes"] = append(nodes, creationAddedNode(op))
	return nodeID, shotID, nil
}

func cloudAgentDirectorWorkstationPosition(nodes []map[string]any) (float64, float64) {
	right, y := math.Inf(-1), 80.0
	for _, node := range nodes {
		position, _ := node["position"].(map[string]any)
		x := numberValue(position["x"], 0)
		if edge := x + numberValue(node["width"], 340); edge > right {
			right, y = edge, math.Max(80, numberValue(position["y"], 80))
		}
	}
	if math.IsInf(right, -1) {
		return 80, 80
	}
	return math.Max(80, right+80), y
}

func cloudAgentDirectorApprovalPreview(plan *cloudAgentDirectorPlan) cloudAgentApprovalPreview {
	preview := plan.Mutation.Preview
	preview.Kind = "director"
	preview.Title = "确认导演台修改"
	preview.Description = strings.Replace(preview.Description, "批准后才会写入当前画布。", "批准后会打开导演台并写入当前画布。", 1)
	return preview
}

// applyCloudAgentDirectorMutation 在 Agent 检查点事务内落库；saveCloudAgentDocument
// 经 saveCreationCanvasWithHistory → SaveAgentCanvasWrite，与浏览器共用 revision 序列。
func applyCloudAgentDirectorMutation(repo *repository.Repository, userID, canvasID, runID string, call cloudAgentCall, policy RuntimePolicySetting, recorder cloudAgentMutationRecorder) (*cloudAgentDirectorPlan, any, error) {
	plan, err := prepareCloudAgentDirectorMutation(repo, userID, canvasID, runID, call)
	if err != nil {
		return nil, nil, err
	}
	mutation := plan.Mutation
	if err := saveCloudAgentDocument(repo, mutation.Canvas, mutation.Document, policy); err != nil {
		return nil, nil, err
	}
	if recorder != nil {
		preview := cloudAgentDirectorApprovalPreview(plan)
		if err := recorder(repo, cloudAgentMutationInput{UserID: userID, CanvasID: canvasID, StepID: call.ID, Operation: mutation.Operation, BeforeSnapshotHash: mutation.BeforeSnapshotHash, AfterSnapshotHash: mutation.AfterSnapshotHash, BeforeJSON: mutation.BeforeJSON, Preview: &preview}); err != nil {
			return nil, nil, err
		}
	}
	return plan, map[string]any{
		"action": plan.Args.Action, "canvasId": canvasID, "nodeId": plan.NodeID, "sceneId": plan.SceneID, "shotId": plan.ShotID,
		"snapshotHash": mutation.ResultSnapshotHash, "canvasSnapshotHash": mutation.AfterSnapshotHash, "beforeCanvasSnapshotHash": mutation.BeforeSnapshotHash,
		"committed": true, "directorOpened": true, "summary": cloudAgentDirectorApprovalPreview(plan).Description,
	}, nil
}

func cloudAgentDirectorOpenPayload(canvasID, nodeID, sceneID, shotID, action, requestID string) map[string]any {
	payload := map[string]any{"canvasId": canvasID, "nodeId": nodeID, "sceneId": sceneID, "action": action}
	if shotID != "" {
		payload["shotId"] = shotID
	}
	if requestID != "" {
		payload["requestId"] = requestID
	}
	return payload
}

// prepareCloudAgentDirectorPreview 校验场景、镜头与摄影机并冻结预演参数。
func prepareCloudAgentDirectorPreview(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentDirectorPreview, error) {
	args, err := cloudAgentDirectorDecode(call)
	if err != nil {
		return nil, err
	}
	if args.Template != "" || args.Title != "" || len(args.Patch) > 0 {
		return nil, cloudAgentFieldError("patch", "unexpected_field", "preview 只接受 nodeId、sceneId、shotId 和 expectedSceneHash")
	}
	_, doc, err := cloudAgentPrevisCanvas(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	scene, nodeID, err := cloudAgentDirectorResolveScene(doc, args)
	if err != nil {
		return nil, err
	}
	if scene == nil {
		return nil, cloudAgentFieldError("sceneId", "required", "preview 必须指定 sceneId 或已绑定场景的 nodeId")
	}
	if args.ExpectedSceneHash != "" && args.ExpectedSceneHash != creationHash(scene) {
		return nil, &cloudAgentFieldArgumentError{error: &cloudAgentArgumentError{creationConflict("导演场景已变化，本次未预演；请重新读取")}, Field: "expectedSceneHash", Issue: "stale_snapshot"}
	}
	shotID := args.ShotID
	if shotID == "" {
		shotID = stringValue(scene["activeShotId"])
	}
	encoded, _ := json.Marshal(map[string]any{"sceneId": stringValue(scene["id"]), "shotId": shotID})
	inner := cloudAgentCall{ID: call.ID}
	inner.Function.Name, inner.Function.Arguments = cloudAgentDirectorTool, string(encoded)
	result, err := cloudAgentPrevisPreview(repo, userID, canvasID, inner)
	if err != nil {
		return nil, err
	}
	fields := result.(map[string]any)
	return &cloudAgentDirectorPreview{
		CallID: call.ID, CanvasID: canvasID, NodeID: nodeID, SceneID: stringValue(fields["sceneId"]), ShotID: shotID,
		Duration: numberValue(fields["duration"], 5), FPS: int(numberValue(fields["fps"], 24)),
	}, nil
}

func cloudAgentDirectorPreviewPending(state *cloudAgentRuntime, call cloudAgentCall) bool {
	return state != nil && state.DirectorPreview != nil && state.DirectorPreview.CallID == call.ID && call.Function.Name == cloudAgentDirectorTool
}

func cloudAgentDirectorPreviewReceipt(preview *cloudAgentDirectorPreview, status string) map[string]any {
	return map[string]any{"action": "preview", "status": status, "requestId": preview.RequestID, "canvasId": preview.CanvasID, "nodeId": preview.NodeID, "sceneId": preview.SceneID, "shotId": preview.ShotID, "duration": preview.Duration, "fps": preview.FPS, "execution": "browser_previs_viewport"}
}

// startCloudAgentDirectorPreview 发出 director_open 并把调用挂起为 awaiting_viewport。
// 等待只在检查点里：浏览器回传或超时都由下一次推进处理。
func (s *Service) startCloudAgentDirectorPreview(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		preview, err := prepareCloudAgentDirectorPreview(repo, run.UserID, state.Request.CanvasID, call)
		if err != nil {
			if !cloudAgentToolErrorIsBusiness(err) {
				return err
			}
			cloudAgentRecordToolResult(current, state, call, nil, err)
			return cloudAgentSave(current, state)
		}
		preview.RequestID = "dpv_" + cloudAgentID(run.UserID, run.ID+":director-preview:"+state.PiToolBatchTaskID+fmt.Sprintf(":%d:", state.Step)+call.ID)[2:]
		preview.Deadline = time.Now().UTC().Add(cloudAgentDirectorPreviewTimeout())
		state.DirectorPreview = preview
		state.event(run.ID, "director_open", cloudAgentDirectorOpenPayload(preview.CanvasID, preview.NodeID, preview.SceneID, preview.ShotID, "preview", preview.RequestID))
		receipt := cloudAgentDirectorPreviewReceipt(preview, "awaiting_viewport")
		receipt["callId"], receipt["toolName"], receipt["deadline"] = call.ID, call.Function.Name, preview.Deadline
		state.event(run.ID, "director_preview_requested", receipt)
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "waiting_tool", cloudAgentDirectorPreviewWaitKind, preview.RequestID, cloudAgentDirectorPreviewWaitLabel
		return cloudAgentSave(current, state)
	})
}

// advanceCloudAgentDirectorPreview 只处理超时：未到期时什么都不写，保持挂起。
func (s *Service) advanceCloudAgentDirectorPreview(run *model.CloudAgentExecution, state *cloudAgentRuntime, call cloudAgentCall) error {
	if time.Now().UTC().Before(state.DirectorPreview.Deadline) {
		return nil
	}
	return s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		preview := state.DirectorPreview
		result := cloudAgentDirectorPreviewReceipt(preview, "timed_out")
		result["reason"], result["retryable"] = cloudAgentDirectorViewportReason, true
		result["guidance"] = "导演台页面未打开或未在时限内完成录制；请提示用户打开画布中的导演台后再预览，或继续其他工作"
		state.DirectorPreview = nil
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
		cloudAgentRecordToolResult(current, state, call, result, nil)
		return cloudAgentSave(current, state)
	})
}

// SubmitCloudAgentDirectorPreview 接收浏览器导演台的录制结果并恢复挂起的 director 调用。
// 运行归属、requestId 与资源归属都在这里校验；videoResourceId 与 errorMessage 必须二选一。
func (s *Service) SubmitCloudAgentDirectorPreview(userID, runID, requestID, videoResourceID, errorMessage string) error {
	notFound := &kernel.AppError{Status: kernel.CodeNotFound, Code: kernel.CodeNotFound, Reason: cloudAgentDirectorPreviewNotFound, Message: "导演台预演请求不存在或已过期"}
	requestID, videoResourceID, errorMessage = strings.TrimSpace(requestID), strings.TrimSpace(videoResourceID), strings.TrimSpace(errorMessage)
	if requestID == "" || len(requestID) > 120 {
		return notFound
	}
	if (videoResourceID == "") == (errorMessage == "") {
		return BadAuthRequest("videoResourceId 与 error 必须且只能提供一个")
	}
	if utf8.RuneCountInString(errorMessage) > cloudAgentDirectorPreviewErrorMax || strings.ContainsAny(errorMessage, "\x00\r\n") {
		return BadAuthRequest("error 无效")
	}
	defer s.wakeAgentTools()
	// 工具推进可能同时在写同一运行；revision 冲突时重读后再试，避免把并发误报给浏览器。
	for attempt := 0; ; attempt++ {
		err := s.submitCloudAgentDirectorPreviewOnce(userID, runID, requestID, videoResourceID, errorMessage, notFound)
		if !errors.Is(err, repository.ErrCreationConflict) || attempt >= 2 {
			return err
		}
	}
}

func (s *Service) submitCloudAgentDirectorPreviewOnce(userID, runID, requestID, videoResourceID, errorMessage string, notFound error) error {
	run, err := s.repo.CloudAgent(userID, runID)
	if err != nil {
		return notFound
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		// 无法解析的运行不可能有挂起的预演请求。
		return notFound
	}
	preview := state.DirectorPreview
	if cloudAgentRunTerminal(run.Status) || preview == nil || preview.RequestID != requestID || state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != preview.CallID || !time.Now().UTC().Before(preview.Deadline) {
		return notFound
	}
	call := state.Calls[state.CallIndex]
	var result map[string]any
	var toolErr error
	if videoResourceID != "" {
		resource, err := s.repo.ResourceForUser(userID, videoResourceID)
		if err != nil || resource == nil || resource.Status != model.ResourceStatusReady || resource.Size <= 0 || !strings.HasPrefix(resource.MimeType, "video/") {
			return BadAuthRequest("预演视频资源不存在、未就绪或不属于当前账号")
		}
		result = cloudAgentDirectorPreviewReceipt(preview, "completed")
		result["videoResourceId"] = resource.ID
		result["text"] = "导演台已在浏览器视口录制白模预演并保存为视频资源"
	} else {
		toolErr = BadAuthRequest("导演台预演录制失败：" + errorMessage)
	}
	return s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state.DirectorPreview = nil
		current.RuntimePhase, current.WaitKind, current.WaitID, current.WaitReason = "running", "", "", ""
		cloudAgentRecordToolResult(current, &state, call, result, toolErr)
		return cloudAgentSave(current, &state)
	})
}

func cloudAgentDirectorPreviewAction(call cloudAgentCall) bool {
	var args struct {
		Action string `json:"action"`
	}
	return json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Action == "preview"
}

// cloudAgentDirectorApproval 生成 request_approval 下的审批卡；preview 只校验目标，
// 不写画布，批准后再挂起等待浏览器视口。
func cloudAgentDirectorApproval(repo *repository.Repository, userID, canvasID, runID string, call cloudAgentCall) (cloudAgentApprovalPreview, error) {
	if cloudAgentDirectorPreviewAction(call) {
		preview, err := prepareCloudAgentDirectorPreview(repo, userID, canvasID, call)
		if err != nil {
			return cloudAgentApprovalPreview{}, err
		}
		return cloudAgentApprovalPreview{Kind: "director", Title: "确认导演台预演", Description: "批准后会打开导演台，在浏览器视口录制白模预演视频；页面未打开时会超时返回。", Items: []cloudAgentApprovalPreviewItem{{Operation: "director_preview", NodeID: preview.NodeID, Summary: fmt.Sprintf("预演场景 %s 的镜头 %s", preview.SceneID, preview.ShotID)}}}, nil
	}
	plan, err := prepareCloudAgentDirectorMutation(repo, userID, canvasID, runID, call)
	if err != nil {
		return cloudAgentApprovalPreview{}, err
	}
	return cloudAgentDirectorApprovalPreview(plan), nil
}
