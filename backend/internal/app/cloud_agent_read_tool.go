package app

import (
	"context"
	"errors"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/repository"
)

// cloudAgentReadTool dispatches read-only tools. Write tools never reach this
// path; director writes are rejected explicitly.
func cloudAgentReadTool(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall, services ...*Service) (any, error) {
	var service *Service
	if len(services) > 0 {
		service = services[0]
	}
	switch call.Function.Name {
	case "canvas_preflight":
		return cloudAgentHandleCanvasPreflight(repo, userID, state, call, services...)
	case "director":
		if state == nil || cloudAgentWriteCall(call) {
			return nil, BadAuthRequest("director 写操作不能走只读通道")
		}
		return cloudAgentDirectorRead(repo, userID, state.Request.CanvasID, call)
	case "web_search":
		return cloudAgentReadWebSearch(service, state, call)
	case "plan_update":
		return cloudAgentApplyPlanUpdate(state, call)
	case "ask_user":
		return cloudAgentAskUser(call)
	case "canvas_list_node_types":
		return cloudAgentReadNodeTypes(call)
	case "canvas_list_assets":
		return cloudAgentListAssets(repo, userID, call)
	case "canvas_read_content", "canvas_search_nodes":
		return cloudAgentContentRead(repo, userID, state.Request.CanvasID, call)
	case "canvas_get_state":
		return cloudAgentReadCanvasState(repo, userID, state, call)
	case "storyboard":
		return cloudAgentReadStoryboardTool(repo, userID, state, call)
	case "canvas_read_batch_table":
		return cloudAgentReadBatchTable(repo, userID, state, call)
	case "model_list":
		if len(services) == 0 || services[0] == nil {
			return nil, errors.New("Agent 模型目录工具缺少服务上下文")
		}
		service := services[0]
		return service.cloudAgentQueryModels(userID, state.Request.CanvasID, call.Function.Arguments)
	case "task_get":
		return cloudAgentReadTask(repo, userID, state, call)
	}
	return nil, BadAuthRequest("未知工具")
}

func cloudAgentReadWebSearch(service *Service, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	if service == nil || state == nil || !state.Request.WebSearchEnabled {
		return nil, kernel.Forbidden("本轮未启用联网搜索")
	}
	var args struct {
		Query string `json:"query"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	return service.agentWebSearch(args.Query)
}

func cloudAgentReadNodeTypes(call cloudAgentCall) (any, error) {
	var args struct {
		NodeType string `json:"nodeType"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	return cloudAgentNodeTypesFiltered(args.NodeType)
}

type cloudAgentCanvasStateReadArgs struct {
	View             string   `json:"view"`
	Connections      string   `json:"connections"`
	Offset           int      `json:"offset"`
	ConnectionOffset int      `json:"connectionOffset"`
	NodeIDs          []string `json:"nodeIds"`
	FocusNodeIDs     []string `json:"focusNodeIds"`
	Depth            *int     `json:"depth"`
	IncludeRelated   *bool    `json:"includeRelated"`
	StoryboardOffset int      `json:"storyboardOffset"`
}

func cloudAgentReadCanvasState(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	var args cloudAgentCanvasStateReadArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if args.View == "media_versions" {
		return cloudAgentReadMediaVersions(repo, userID, state, args)
	}
	depth, includeRelated, err := cloudAgentCanvasStateDepth(args)
	if err != nil {
		return nil, err
	}
	read, err := cloudAgentCanvasReadView(args.View, args.Connections, len(args.NodeIDs) > 0 || len(args.FocusNodeIDs) > 0, len(args.FocusNodeIDs) > 0)
	if err != nil {
		return nil, err
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	if includeRelated {
		depth = 0
	}
	return cloudAgentCanvasStatePageSelected(repo, userID, state.Request.CanvasID, doc, args.Offset, args.NodeIDs, args.FocusNodeIDs, depth, includeRelated, args.StoryboardOffset, args.ConnectionOffset, 0, read)
}

func cloudAgentReadMediaVersions(repo *repository.Repository, userID string, state *cloudAgentRuntime, args cloudAgentCanvasStateReadArgs) (any, error) {
	if len(args.NodeIDs) != 1 || len(args.FocusNodeIDs) != 0 || args.Depth != nil || args.Offset != 0 || args.ConnectionOffset != 0 || args.StoryboardOffset != 0 || args.IncludeRelated != nil || args.Connections != "" {
		return nil, BadAuthRequest("media_versions 需要一个 nodeIds，不接收画布分页或连线参数")
	}
	cs := commands.NewService(repo, commands.ServiceOptions{})
	p := identity.Principal{ID: userID, OwnerID: userID, Role: "owner", Scopes: identity.OwnerScopes()}
	page, err := cs.ReadMediaVersions(context.Background(), p, state.Request.CanvasID, args.NodeIDs[0])
	if err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	return map[string]any{"nodeId": args.NodeIDs[0], "mediaVersions": page, "snapshotHash": cloudAgentCanvasHash(doc)}, nil
}

// cloudAgentCanvasStateDepth validates paging and subgraph arguments and
// returns the subgraph depth (focus reads default to 1) and includeRelated.
func cloudAgentCanvasStateDepth(args cloudAgentCanvasStateReadArgs) (int, bool, error) {
	depth := 0
	if args.Depth != nil {
		depth = *args.Depth
	}
	if len(args.FocusNodeIDs) > 0 && args.Depth == nil {
		depth = 1
	}
	if args.Offset < 0 || args.ConnectionOffset < 0 || args.StoryboardOffset < 0 {
		return 0, false, &cloudAgentArgumentError{BadAuthRequest("画布读取参数无效：offset、connectionOffset、storyboardOffset 必须是非负整数")}
	}
	if len(args.NodeIDs) > 8 || len(args.FocusNodeIDs) > 8 {
		return 0, false, &cloudAgentArgumentError{BadAuthRequest("画布读取参数无效：nodeIds 与 focusNodeIds 最多包含8个节点ID")}
	}
	if len(args.NodeIDs) > 0 && len(args.FocusNodeIDs) > 0 {
		return 0, false, &cloudAgentArgumentError{BadAuthRequest("画布读取参数无效：nodeIds 与 focusNodeIds 互斥")}
	}
	if depth < 0 || depth > 3 {
		return 0, false, &cloudAgentArgumentError{BadAuthRequest("画布读取参数无效：depth 必须是0到3")}
	}
	if len(args.FocusNodeIDs) == 0 && args.Depth != nil {
		return 0, false, &cloudAgentArgumentError{BadAuthRequest("画布读取参数无效：depth 只能与 focusNodeIds 一起使用")}
	}
	includeRelated := args.IncludeRelated != nil && *args.IncludeRelated
	if includeRelated && len(args.FocusNodeIDs) == 0 {
		return 0, false, &cloudAgentArgumentError{BadAuthRequest("画布读取参数无效：includeRelated 只能与 focusNodeIds 一起使用")}
	}
	if includeRelated && args.Depth != nil {
		return 0, false, &cloudAgentArgumentError{BadAuthRequest("画布读取参数无效：includeRelated 与 depth 不能同时使用")}
	}
	return depth, includeRelated, nil
}

func cloudAgentReadStoryboardTool(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	fields, action, err := storyboardArguments(call)
	if err != nil {
		return nil, err
	}
	if action != "read" {
		return nil, BadAuthRequest("此路径只接受 storyboard.read")
	}
	result, readErr := cloudAgentReadStoryboard(repo, userID, state.Request.CanvasID, storyboardCallFields(call, fields, "operationIds", "nodeId", "view", "mode", "offset", "rows", "rowIds", "fields", "missingAnyFields"))
	if resultMap, ok := result.(map[string]any); ok && fields["view"] == nil && fields["fields"] == nil {
		resultMap["markdown"] = cloudAgentStoryboardMarkdown(resultMap)
	}
	return result, readErr
}

func cloudAgentReadBatchTable(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	var args struct {
		NodeID string `json:"nodeId"`
		Offset int    `json:"offset"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if err := validateCloudAgentID(args.NodeID, "批量创作表节点ID", 80); err != nil || args.Offset < 0 {
		return nil, BadAuthRequest("批量创作表节点ID或分页参数无效")
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	if _, _, _, _, err := batchTableNodeFromDocument(doc, args.NodeID); err != nil {
		return nil, err
	}
	view, err := cloudAgentCanvasState(repo, userID, state.Request.CanvasID, doc, 0, []string{args.NodeID}, args.Offset)
	if err != nil {
		return nil, err
	}
	return cloudAgentBatchTableReadResult(view, args.NodeID)
}

func cloudAgentReadTask(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	var args struct {
		TaskID string `json:"taskId"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	task, err := repo.TaskForUser(userID, args.TaskID)
	if err != nil {
		return nil, err
	}
	if task.ProjectID != state.Request.CanvasID {
		return nil, BadAuthRequest("不能读取其他画布的任务")
	}
	result := cloudAgentTaskDiagnostic(repo, task)
	result["status"] = task.Status
	result["text"] = truncateRunes(taskResultText(task.ResultJSON), 4000)
	return result, nil
}
