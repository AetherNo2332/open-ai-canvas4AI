package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// syncMapForTest 让每个用例从"没有拒绝记录"的干净状态开始（进程内缓存是包级变量）。
func syncMapForTest() sync.Map {
	return sync.Map{}
}

func strictToolBody(t *testing.T) map[string]interface{} {
	t.Helper()
	return map[string]interface{}{
		"model": "model",
		"tools": []interface{}{
			map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "canvas_get_state", "parameters": map[string]interface{}{"type": "object"}}},
			map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "canvas_apply_ops", "parameters": map[string]interface{}{"type": "object"}}},
		},
	}
}

func TestApplyAndStripAgentStrictTools(t *testing.T) {
	body := strictToolBody(t)
	applyAgentStrictTools(body, false)
	tools := body["tools"].([]interface{})
	for _, item := range tools {
		if _, exists := item.(map[string]interface{})["function"].(map[string]interface{})["strict"]; exists {
			t.Fatal("未声明支持时不应下发 strict")
		}
	}
	applyAgentStrictTools(body, true)
	for _, item := range tools {
		if item.(map[string]interface{})["function"].(map[string]interface{})["strict"] != true {
			t.Fatalf("声明支持时应给每个工具加 strict：%+v", item)
		}
	}
	if !stripAgentStrictTools(body) {
		t.Fatal("回退应报告确实去掉了 strict")
	}
	if stripAgentStrictTools(body) {
		t.Fatal("没有 strict 时回退不应报告改动")
	}
	for _, item := range tools {
		if _, exists := item.(map[string]interface{})["function"].(map[string]interface{})["strict"]; exists {
			t.Fatal("回退后不应残留 strict")
		}
	}
}

func TestAgentStrictToolsCompatibilityErrorDetection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "识别未知参数", err: errors.New(`400 {"error":{"message":"Unrecognized request argument supplied: strict"}}`), want: true},
		{name: "识别不支持", err: errors.New("strict is not supported by this model"), want: true},
		{name: "识别非法取值", err: errors.New(`invalid value for parameter "strict"`), want: true},
		{name: "与 strict 无关的未知参数不回退", err: errors.New("Unrecognized request argument supplied: logprobs"), want: false},
		{name: "空错误", err: nil, want: false},
		{name: "普通上游故障", err: errors.New("upstream timeout"), want: false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := isAgentStrictToolsCompatibilityError(item.err); got != item.want {
				t.Fatalf("= %v, want %v", got, item.want)
			}
		})
	}
}

// 上游拒绝 strict：自动回退一次（不带 strict 重发），并记住这条线路不再尝试。
func TestRunAgentToolTaskFallsBackWhenStrictIsRejected(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	cloudAgentStrictToolRejections = syncMapForTest()
	requests := 0
	sawStrict := []bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests++
		strict := false
		tools, _ := body["tools"].([]interface{})
		for _, item := range tools {
			function, _ := item.(map[string]interface{})["function"].(map[string]interface{})
			if function != nil && function["strict"] == true {
				strict = true
			}
		}
		sawStrict = append(sawStrict, strict)
		if requests == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: strict"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"完成","tool_calls":[]}}]}`))
	}))
	defer server.Close()

	input := canvasGenerationInput{
		Config:        providerConfig{BaseURL: server.URL, APIKey: "key", Model: "model", ChannelID: "channel-1", ChannelModelKey: "strict-model"},
		TextOptions:   canvasTextOptions{StrictTools: true},
		AgentRequests: &agentToolRequests{Canonical: &canonicalAgentRequest{Messages: []map[string]interface{}{{"role": "user", "content": "读画布"}}, Tools: []map[string]interface{}{{"type": "function", "function": map[string]interface{}{"name": "canvas_get_state", "parameters": map[string]interface{}{"type": "object"}}}}, ToolChoice: "auto"}},
	}
	result, err := runAgentToolTask(context.Background(), input)
	if err != nil || result["text"] != "完成" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if requests != 2 || len(sawStrict) != 2 || !sawStrict[0] || sawStrict[1] {
		t.Fatalf("兼容回退应去掉 strict：requests=%d saw=%v", requests, sawStrict)
	}
	if !cloudAgentStrictToolsRejected("channel-1", "strict-model") {
		t.Fatal("拒绝过的线路应被记住，后续不再尝试 strict")
	}
}

// 能力合同决定是否下发 strict；被拒绝过的线路不再尝试。
func TestCloudAgentStepStrictToolsFollowsCapability(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "strict-model")
	strict := true
	capability.Text.StrictTools = &strict
	row := &model.ChannelModel{ID: "strict-cm", ChannelID: "channel-1", ModelKey: "strict-model", DisplayName: "严格模型", Capability: "text", Protocol: model.ChannelInterfaceChatCompletion, CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, capability), Enabled: true}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	state := &cloudAgentRuntime{Request: CloudAgentRequest{ChannelID: "channel-1", ChannelModelKey: "strict-model"}}
	cloudAgentStrictToolRejections = syncMapForTest()
	if !s.cloudAgentStepStrictTools(state) {
		t.Fatal("声明 strictTools=true 时应下发 strict")
	}
	cloudAgentRememberStrictToolsRejected("channel-1", "strict-model")
	if s.cloudAgentStepStrictTools(state) {
		t.Fatal("被上游拒绝过的线路不应再尝试 strict")
	}
	cloudAgentStrictToolRejections = syncMapForTest()
	// 没有声明时（字段为 nil）不下发
	capability.Text.StrictTools = nil
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "strict-cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, capability)).Error; err != nil {
		t.Fatal(err)
	}
	if s.cloudAgentStepStrictTools(state) {
		t.Fatal("未声明 strictTools 时不应下发 strict")
	}
	if (&Service{}).cloudAgentStepStrictTools(nil) {
		t.Fatal("空状态不应下发 strict")
	}
}
