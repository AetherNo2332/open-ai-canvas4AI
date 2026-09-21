package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type cloudAgentPlanItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func cloudAgentPendingPlanItems(plan []cloudAgentPlanItem) []string {
	pending := make([]string, 0, len(plan))
	for _, item := range plan {
		if item.Status != "done" {
			pending = append(pending, item.Title)
		}
	}
	return pending
}

const cloudAgentPlanBlockMarker = "\n\n## 本轮待办清单\n\n"

func stripCloudAgentPlanBlock(system string) string {
	if i := strings.Index(system, cloudAgentPlanBlockMarker); i >= 0 {
		return system[:i]
	}
	return system
}

func isCloudAgentRuntimeContextMessage(message map[string]any) bool {
	if stringField(message, "role") != "user" {
		return false
	}
	return stringField(message, cloudAgentContextSourceKey) == "runtime"
}

// cloudAgentReattachedContextKinds 是"每一步都会被服务端按最新状态重新拼上去"的运行时消息种类。
var cloudAgentReattachedContextKinds = map[cloudAgentRuntimeContextKind]bool{
	cloudAgentContextPlan:      true,
	cloudAgentContextTaskFacts: true,
}

// cloudAgentReattachedContextMessage 判断这条运行时消息是否属于"每步重拼、可以从历史里丢"的一类。
func cloudAgentReattachedContextMessage(message map[string]any) bool {
	if !isCloudAgentRuntimeContextMessage(message) {
		return false
	}
	var context cloudAgentRuntimeContext
	content := strings.TrimPrefix(stringField(message, "content"), cloudAgentRuntimeContextMarker)
	if json.Unmarshal([]byte(content), &context) != nil {
		return false
	}
	return cloudAgentReattachedContextKinds[context.Kind]
}

// stripCloudAgentRuntimeContext 丢掉尾部"每步重拼"的运行时上下文消息（待办清单、任务与账务事实）：
// 它们每步都会按最新状态重新生成，留在历史里只会越积越多、并让模型把过期状态当现状。
//
// 不能顺手丢掉空输出催办 / 输出纠错 / 参数纠错：那三类是**一次性指令**，只存在于会话里，
// 丢掉就等于重试时模型看不到纠正要求（历史实现只认 plan_state，正是为此）。
func stripCloudAgentRuntimeContext(messages []map[string]any) []map[string]any {
	for len(messages) > 0 && cloudAgentReattachedContextMessage(messages[len(messages)-1]) {
		messages = messages[:len(messages)-1]
	}
	return messages
}

func attachCloudAgentPlan(canonical *canonicalAgentRequest, plan []cloudAgentPlanItem) {
	if canonical == nil {
		return
	}
	canonical.SystemPrompt = stripCloudAgentPlanBlock(canonical.SystemPrompt)
	canonical.Messages = stripCloudAgentRuntimeContext(canonical.Messages)
	if len(plan) == 0 {
		return
	}
	canonical.Messages = append(slices.Clone(canonical.Messages), cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextPlan, Items: plan}))
}

func cloudAgentPlanRequiresFirstApproval(state *cloudAgentRuntime, call cloudAgentCall) bool {
	if state == nil || state.Approval != nil || len(state.Plan) != 0 || state.Request.PermissionMode != "request_approval" {
		return false
	}
	_, ok := cloudAgentPlanApprovalPreview(call)
	return ok
}

func cloudAgentPlanNudgeMessage(state *cloudAgentRuntime, pendingTitle string) map[string]any {
	return cloudAgentRuntimeMessage(cloudAgentRuntimeContext{
		Kind: cloudAgentContextPendingPlan, PendingTitle: pendingTitle,
		LatestUserMessage: cloudAgentLatestUserInstruction(state.Canonical.Messages),
	})
}

func cloudAgentPlanApprovalPreview(call cloudAgentCall) (cloudAgentApprovalPreview, bool) {
	var args struct {
		Items []cloudAgentPlanItem `json:"items"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return cloudAgentApprovalPreview{}, false
	}
	items := make([]cloudAgentApprovalPreviewItem, 0, len(args.Items))
	for _, entry := range args.Items {
		title := strings.TrimSpace(entry.Title)
		if title == "" {
			continue
		}
		items = append(items, cloudAgentApprovalPreviewItem{Operation: "plan_step", Summary: truncateRunes(title, 240)})
	}
	if len(items) < 2 {
		return cloudAgentApprovalPreview{}, false
	}
	return cloudAgentApprovalPreview{
		Kind:        "plan",
		Title:       "确认执行计划",
		Description: fmt.Sprintf("Agent 把这轮任务拆成 %d 步。确认后才会开始执行；暂不执行则会让它改用别的做法。", len(items)),
		Items:       items,
	}, true
}

func cloudAgentApplyPlanUpdate(state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	var args struct {
		Items []cloudAgentPlanItem `json:"items"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if len(args.Items) > 20 {
		return nil, BadAuthRequest("待办清单最多 20 项")
	}
	seen := map[string]bool{}
	for i := range args.Items {
		args.Items[i].ID = strings.TrimSpace(args.Items[i].ID)
		args.Items[i].Title = strings.TrimSpace(args.Items[i].Title)
		if args.Items[i].ID == "" || args.Items[i].Title == "" {
			return nil, BadAuthRequest("待办的 id 和 title 不能为空")
		}
		if seen[args.Items[i].ID] {
			return nil, BadAuthRequest("待办 id 重复：" + args.Items[i].ID)
		}
		seen[args.Items[i].ID] = true
		switch args.Items[i].Status {
		case "pending", "doing", "done":
		default:
			args.Items[i].Status = "pending"
		}
	}
	if !slices.Equal(state.Plan, args.Items) {
		state.ActionNudged = false
	}
	state.Plan = args.Items
	return map[string]any{"items": args.Items, "pendingTitles": cloudAgentPendingPlanItems(args.Items)}, nil
}

func cloudAgentAskUser(call cloudAgentCall) (any, error) {
	var args struct {
		Question string `json:"question"`
		Options  []struct {
			Label  string `json:"label"`
			Detail string `json:"detail"`
		} `json:"options"`
		AllowFreeform *bool `json:"allowFreeform"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	question := strings.TrimSpace(args.Question)
	if question == "" {
		return nil, BadAuthRequest("ask_user 必须给出 question：把要用户拍板的那一个问题一句话写清")
	}
	options := make([]map[string]any, 0, len(args.Options))
	for _, option := range args.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		entry := map[string]any{"label": truncateRunes(label, 120)}
		if detail := strings.TrimSpace(option.Detail); detail != "" {
			entry["detail"] = truncateRunes(detail, 240)
		}
		options = append(options, entry)
	}
	if len(options) < 2 {
		return nil, BadAuthRequest("ask_user 至少要给 2 个候选项；若你自己能定，直接做完继续，不要问")
	}
	if len(options) > 6 {
		options = options[:6]
	}
	allowFreeform := true
	if args.AllowFreeform != nil {
		allowFreeform = *args.AllowFreeform
	}
	return map[string]any{
		"phase":         "question",
		"question":      truncateRunes(question, 400),
		"options":       options,
		"allowFreeform": allowFreeform,
	}, nil
}

// skipRemainingCloudAgentCalls 结束本批剩余调用（ask_user 之后本轮不再继续执行）。
// 末尾的 flush 让"本批前面的看图结果"仍能落在全部 tool 结果之后：这批调用到这里已经
// 完整（每个声明的 tool_call_id 都有回执），但本轮就此结束、不会再走 advanceCloudAgent
// 的兜底 flush，少了这一步缓冲的图片会被丢掉。
func skipRemainingCloudAgentCalls(runID string, state *cloudAgentRuntime) {
	for index := state.CallIndex + 1; index < len(state.Calls); index++ {
		cloudAgentToolResult(runID, state, state.Calls[index], map[string]any{"skipped": true}, BadAuthRequest("本轮已结束（等待用户决定），该调用未执行"))
	}
	cloudAgentFlushPendingImages(state)
}

func cloudAgentCanonicalWithPlan(state *cloudAgentRuntime) canonicalAgentRequest {
	canonical := state.Canonical
	attachCloudAgentPlan(&canonical, state.Plan)
	return canonical
}
