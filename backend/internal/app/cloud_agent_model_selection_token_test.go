package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// model_list 的每一项都要带一个服务端签发的 selectionId，并且能解回同一种选择。
func TestCloudAgentModelListIssuesResolvableSelectionID(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	catalog, err := s.cloudAgentModelList("user", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Models) == 0 {
		t.Fatal("模型目录为空")
	}
	seen := 0
	for _, item := range decoded.Models {
		token, _ := item["selectionId"].(string)
		if strings.TrimSpace(token) == "" {
			t.Fatalf("目录项缺少 selectionId：%+v", item)
		}
		selection, err := s.resolveCloudAgentSelectionID("user", token)
		if err != nil {
			t.Fatalf("自己签发的 selectionId 解不开：%v", err)
		}
		expected, _ := item["selection"].(map[string]any)
		if selection.LogicalModelID != stringValue(expected["logicalModelId"]) ||
			selection.ChannelID != stringValue(expected["channelId"]) ||
			selection.ChannelModelKey != stringValue(expected["channelModelKey"]) {
			t.Fatalf("selectionId 与 selection 不一致：%+v / %+v", selection, expected)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("没有校验到任何目录项")
	}
}

// selectionId 是"复制粘贴"契约：被改写、跨用户复用、格式不对都必须拒绝，且报字段级错误。
func TestCloudAgentSelectionIDRejectsForgeryAndCrossUser(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	token, err := s.issueCloudAgentSelectionID("user", cloudAgentSelection{ChannelID: "channel", ChannelModelKey: "seedance-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveCloudAgentSelectionID("user", token); err != nil {
		t.Fatalf("合法凭证应解开：%v", err)
	}
	payload, signature, _ := strings.Cut(token, ".")
	forgedPayload := payload[:len(payload)-2] + "AA"
	cases := []struct {
		name  string
		token string
		user  string
	}{
		{name: "签名不符", token: payload + "." + signature[:len(signature)-2] + "AA", user: "user"},
		{name: "内容被改写", token: forgedPayload + "." + signature, user: "user"},
		{name: "跨用户复用", token: token, user: "other"},
		{name: "缺少分隔符", token: payload, user: "user"},
		{name: "空凭证", token: "", user: "user"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, err := s.resolveCloudAgentSelectionID(item.user, item.token)
			var fieldErr *cloudAgentFieldArgumentError
			if !errors.As(err, &fieldErr) || fieldErr.Field != "selectionId" {
				t.Fatalf("应回 selectionId 字段级错误：%v", err)
			}
		})
	}
}

// 归一化：只给 selectionId 也能通过校验，并且填回三个字段供下游使用。
func TestCloudAgentModelSelectionAcceptsSelectionID(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	token, err := s.issueCloudAgentSelectionID("user", cloudAgentSelection{ChannelID: "channel", ChannelModelKey: "seedance-test"})
	if err != nil {
		t.Fatal(err)
	}
	var args cloudAgentMediaArgs
	raw := `{"selectionId":"` + token + `"}`
	if err := decodeCloudAgentJSONObject(raw, &args); err != nil {
		t.Fatal(err)
	}
	if err := s.validateCloudAgentModelSelection("user", raw, &args); err != nil {
		t.Fatalf("selectionId 应被接受：%v", err)
	}
	if args.ChannelID != "channel" || args.ChannelModelKey != "seedance-test" || args.LogicalModelID != "" {
		t.Fatalf("selectionId 没有归一化回三个字段：%+v", args)
	}
}

// 与旧契约混用、类型不对都必须拒绝（不静默选一个）。
func TestCloudAgentModelSelectionRejectsSelectionIDConflicts(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	token, err := s.issueCloudAgentSelectionID("user", cloudAgentSelection{LogicalModelID: "model"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		raw   string
		field string
		issue string
	}{
		{name: "与 logicalModelId 混用", raw: `{"selectionId":"` + token + `","logicalModelId":"model"}`, field: "selectionId", issue: "mutually_exclusive"},
		{name: "与 channelId 混用", raw: `{"selectionId":"` + token + `","channelId":"channel"}`, field: "selectionId", issue: "mutually_exclusive"},
		{name: "selectionId 为 null", raw: `{"selectionId":null}`, field: "selectionId", issue: "type_mismatch"},
		{name: "selectionId 被改写", raw: `{"selectionId":"` + token + `x"}`, field: "selectionId", issue: "invalid_value"},
		{name: "selectionId 类型不对", raw: `{"selectionId":7}`},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			call := cloudAgentCall{ID: "selection-call"}
			call.Function.Name, call.Function.Arguments = "generate_media", item.raw
			_, _, err := s.prepareCloudAgentMedia(nil, nil, call)
			var argumentErr *cloudAgentArgumentError
			if !errors.As(err, &argumentErr) {
				t.Fatalf("应是可纠正的参数错误：%v", err)
			}
			if item.field == "" {
				return
			}
			var fieldErr *cloudAgentFieldArgumentError
			if !errors.As(err, &fieldErr) || fieldErr.Field != item.field || fieldErr.Issue != item.issue {
				t.Fatalf("结论 = %v，期望 %s/%s", err, item.field, item.issue)
			}
		})
	}
}
