package app

import (
	"context"
	"errors"
	"testing"
)

// handoff 工作项 B 的第一步：把"模型输出问题"与"权限/状态/上游"分开，并且只加标注不改行为。
func TestCloudAgentToolErrorClass(t *testing.T) {
	canvasRequest := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}
	readOnlyRequest := CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}}
	scopedRequest := CloudAgentRequest{PermissionMode: "auto"} // contextScope 为空：只暴露通用工具
	call := func(name string) cloudAgentCall {
		var c cloudAgentCall
		c.Function.Name = name
		return c
	}

	cases := []struct {
		name          string
		request       CloudAgentRequest
		call          cloudAgentCall
		err           error
		allowed       bool
		wantClass     string
		wantRetryable bool
		wantAction    string
	}{
		{"幻觉工具名", canvasRequest, call("make_coffee"), BadAuthRequest("工具未获本轮权限授权"), false, cloudAgentToolErrorInvalidModelOutput, true, "use_advertised_tools"},
		{"contextScope 为空却调画布工具", scopedRequest, call("canvas_get_state"), BadAuthRequest("工具未获本轮权限授权"), false, cloudAgentToolErrorInvalidModelOutput, true, "use_advertised_tools"},
		{"只读轮里发起生成", readOnlyRequest, call("generate_media"), BadAuthRequest("工具未获本轮权限授权"), false, cloudAgentToolErrorPermission, false, "ask_user"},
		{"参数不符合契约", canvasRequest, call("canvas_get_state"), cloudAgentJSONArgumentError(errors.New("unknown field")), true, cloudAgentToolErrorSchemaError, true, "fix_arguments"},
		{"字段级参数错误", canvasRequest, call("canvas_apply_ops"), cloudAgentFieldError("snapshotHash", "required", "缺 snapshotHash"), true, cloudAgentToolErrorSchemaError, true, "fix_arguments"},
		{"画布已变化", canvasRequest, call("canvas_apply_ops"), cloudAgentSnapshotConflictError("画布已变化"), true, cloudAgentToolErrorStateConflict, true, "reread_canvas"},
		{"上游 5xx", canvasRequest, call("task_get"), providerHTTPError{StatusCode: 503}, true, cloudAgentToolErrorUpstream, true, "report_to_user"},
		{"上游 4xx", canvasRequest, call("task_get"), providerHTTPError{StatusCode: 400}, true, cloudAgentToolErrorUpstream, false, "report_to_user"},
		{"超时", canvasRequest, call("task_get"), context.DeadlineExceeded, true, cloudAgentToolErrorUpstream, true, "report_to_user"},
		{"其它", canvasRequest, call("task_get"), errors.New("说不清"), true, cloudAgentToolErrorUnknown, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, retryable, action := cloudAgentToolErrorClass(tc.request, tc.call, tc.err, tc.allowed)
			if class != tc.wantClass || retryable != tc.wantRetryable || action != tc.wantAction {
				t.Fatalf("class=%q retryable=%v action=%q，期望 %q/%v/%q", class, retryable, action, tc.wantClass, tc.wantRetryable, tc.wantAction)
			}
			if label := cloudAgentToolErrorLabel(class); label == "" {
				t.Fatalf("归类 %q 缺少人读标签", class)
			}
		})
	}
}

// 归类必须落到工具失败事件上：payload 与回执都要带，界面与诊断包不用再猜。
func TestCloudAgentToolResultCarriesErrorClass(t *testing.T) {
	state := &cloudAgentRuntime{Request: CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}, Events: []CloudAgentEvent{}}
	var call cloudAgentCall
	call.ID = "call-1"
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = "{}"
	cloudAgentToolResult("run-1", state, call, nil, cloudAgentSnapshotConflictError("画布已变化，本次未写入；请重新读取并重新申请审批"))

	var payload map[string]any
	for _, event := range state.Events {
		if event.Type == "tool_failed" {
			payload = event.Payload
		}
	}
	if payload == nil || payload["errorClass"] != cloudAgentToolErrorStateConflict {
		t.Fatalf("tool_failed 载荷缺少归类：%+v", payload)
	}
	detail, _ := payload["result"].(map[string]any)
	if detail["errorClassLabel"] != "画布状态已变化" || detail["retryable"] != true || detail["requiredAction"] != "reread_canvas" {
		t.Fatalf("回执里的归类字段不对：%+v", detail)
	}
}
