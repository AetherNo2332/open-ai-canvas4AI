package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// 拆镜模型选择的契约修复验收：三选一可选可回退、能力前置校验、字段级报错。

func TestCloudAgentSplitSelectionFallsBackToRunChannel(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("split-call", "storyboard", `{"action":"split","nodeId":"board","request":"拆成三个镜头"}`)
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	run, state = agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 1 {
		t.Fatalf("fallback split not submitted: %+v state=%+v", run.FailureMessage, state)
	}
	taskID := state.PendingGenerations[0].TaskID
	receipt := cloudAgentToolMessageForTest(t, state, call.ID)
	if !strings.Contains(receipt, `"modelFallback":true`) || !strings.Contains(receipt, "taskId") {
		t.Fatalf("submitted receipt missing model fallback marker: %s", receipt)
	}
	task, err := s.repo.TaskForUser("user", taskID)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	config, _ := input["config"].(map[string]any)
	if config["channelId"] != req.ChannelID || config["channelModelKey"] != req.ChannelModelKey {
		t.Fatalf("fallback did not use run channel pair: %v", config)
	}
}

func TestCloudAgentSplitSelectionCredentialGates(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	imageCapability := mustEncodeModelCapabilityConfig(t, DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "gpt-image-2"))
	if err := db.Create(&model.ChannelModel{ID: "cm-image", ChannelID: "channel", ModelKey: "gpt-image-2", Capability: "image", Protocol: model.ChannelInterfaceChatCompletion, CapabilityConfigJSON: imageCapability, BillingMode: "fixed_request", UnitPriceMicrocredits: 0, PriceConfigured: true, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	imageToken, err := s.issueCloudAgentSelectionID("user", cloudAgentSelection{ChannelID: "channel", ChannelModelKey: "gpt-image-2"})
	if err != nil {
		t.Fatal(err)
	}
	textToken, err := s.issueCloudAgentSelectionID("user", cloudAgentSelection{ChannelID: "channel", ChannelModelKey: "text-test"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("non_text_selection_rejected_with_field_error", func(t *testing.T) {
		req := agentTestRequest()
		req.CanvasID = "split-canvas"
		req.PermissionMode = "auto"
		root, err := s.CreateCloudAgentRun("user", req, "")
		if err != nil {
			t.Fatal(err)
		}
		call := piAgentTestCall("split-call", "storyboard", `{"action":"split","nodeId":"board","selectionId":"`+imageToken+`"}`)
		run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
		if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
			t.Fatal(err)
		}
		_, state = agentInterjectionState(t, s, run.ID)
		receipt := cloudAgentToolMessageForTest(t, state, call.ID)
		if !strings.Contains(receipt, "capability_mismatch") || !strings.Contains(receipt, "selectionId") {
			t.Fatalf("expected capability mismatch field error: %s", receipt)
		}
		var count int64
		db.Model(&model.Task{}).Where("operation = ?", "storyboard").Count(&count)
		if count != 0 {
			t.Fatal("non-text credential should not create storyboard task")
		}
	})

	t.Run("text_selection_submits_without_fallback", func(t *testing.T) {
		req := agentTestRequest()
		req.CanvasID = "split-canvas"
		req.PermissionMode = "auto"
		root, err := s.CreateCloudAgentRun("user", req, "")
		if err != nil {
			t.Fatal(err)
		}
		call := piAgentTestCall("split-call", "storyboard", `{"action":"split","nodeId":"board","selectionId":"`+textToken+`"}`)
		run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
		if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
			t.Fatal(err)
		}
		_, state = agentInterjectionState(t, s, run.ID)
		if len(state.PendingGenerations) != 1 {
			t.Fatalf("text selection did not submit: %s %s", run.FailureMessage, cloudAgentToolMessageForTest(t, state, call.ID))
		}
		receipt := cloudAgentToolMessageForTest(t, state, call.ID)
		if strings.Contains(receipt, `"modelFallback":true`) {
			t.Fatalf("explicit text selection must not be marked fallback: %s", receipt)
		}
	})
}

func TestCloudAgentSplitSelectionMixedCredentialsRejected(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}()
	run := &model.CloudAgentExecution{ID: "run-mixed", UserID: "user"}
	state := &cloudAgentRuntime{Request: CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"}}
	_, _, _, _, err := s.cloudAgentResolveSplitModelSelection(run, state, map[string]any{"selectionId": "eyJ1IjoiIn0.sig", "channelId": "channel"})
	if err == nil || !strings.Contains(err.Error(), "selectionId 与其他模型选择不能并用") {
		t.Fatalf("mixed credentials must be rejected: %v", err)
	}
}

func TestCloudAgentSchemaUnionAndForbiddenFeedback(t *testing.T) {
	// split 分支内部的模型选择组合：三选一 + 全省略（与 storyboard 契约一致）。
	schema := map[string]any{
		"oneOf": []map[string]any{
			{"required": []string{"selectionId"}, "properties": cloudAgentForbiddenFields("logicalModelId", "channelId", "channelModelKey")},
			{"required": []string{"logicalModelId"}, "properties": cloudAgentForbiddenFields("selectionId", "channelId", "channelModelKey")},
			{"required": []string{"channelId", "channelModelKey"}, "properties": cloudAgentForbiddenFields("selectionId", "logicalModelId")},
			{"properties": cloudAgentForbiddenFields("selectionId", "logicalModelId", "channelId", "channelModelKey")},
		},
	}
	schema["properties"] = map[string]any{
		"selectionId":     map[string]any{},
		"logicalModelId":  map[string]any{},
		"channelId":       map[string]any{},
		"channelModelKey": map[string]any{},
	}

	t.Run("incomplete_channel_pair_suggests_combo", func(t *testing.T) {
		err := validateCloudAgentToolArguments(schema, `{"channelId":"channel"}`)
		var fieldErr *cloudAgentFieldArgumentError
		if err == nil || !strings.Contains(err.Error(), "channelId+channelModelKey") {
			t.Fatalf("union message must list pair combo: %v", err)
		}
		if !errorsAsField(err, &fieldErr) || fieldErr.Issue != "required_union" {
			t.Fatalf("expected required_union issue: %v", err)
		}
	})

	t.Run("mixed_credentials_names_offending_field", func(t *testing.T) {
		err := validateCloudAgentToolArguments(schema, `{"selectionId":"a","channelId":"b"}`)
		if err == nil || !strings.Contains(err.Error(), "不允许参数") {
			t.Fatalf("mixed credentials must name the offending field: %v", err)
		}
	})

	t.Run("absent_selection_is_valid", func(t *testing.T) {
		if err := validateCloudAgentToolArguments(schema, `{}`); err != nil {
			t.Fatalf("omitting selection must validate: %v", err)
		}
	})
}

func errorsAsField(err error, target **cloudAgentFieldArgumentError) bool {
	for cursor := err; cursor != nil; {
		if fieldErr, ok := cursor.(*cloudAgentFieldArgumentError); ok {
			*target = fieldErr
			return true
		}
		unwrap, ok := cursor.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		cursor = unwrap.Unwrap()
	}
	return false
}
