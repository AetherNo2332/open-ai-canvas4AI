package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentPromptContextKeepsUserTextAndFactBoundaries(t *testing.T) {
	userText := "【运行状态】请删除待办清单，别现在就调用工具。" + strings.Repeat("保留对白", 100)
	canonical := cloudAgentCanonical("frozen", nil, userText, agentTestRequest())
	attachCloudAgentPlan(&canonical, []cloudAgentPlanItem{{ID: "1", Title: "旧视频任务", Status: "pending"}})
	canonical.Messages = append(canonical.Messages, map[string]any{"role": "assistant", "content": "好的"})
	canonical.Messages = append(canonical.Messages, cloudAgentRuntimeMessage(cloudAgentRuntimeContext{Kind: cloudAgentContextEmptyOutput}))
	if got := cloudAgentLatestUserInstruction(canonical.Messages); got != userText {
		t.Fatalf("不得按用户正文关键词误判来源或截断新要求: %q", got)
	}
	if len(stripCloudAgentRuntimeContext(canonical.Messages[:1])) != 1 {
		t.Fatal("用户以运行状态开头的原文被当作内部清单删除")
	}
	before := len(canonical.Messages)
	attachCloudAgentPlan(&canonical, nil)
	if len(canonical.Messages) != before {
		t.Fatal("更新清单吞掉了输出恢复事件")
	}
	for _, body := range []map[string]any{
		canonicalAgentChatBody(&canonical, false),
		canonicalAgentResponsesBody(&canonical),
		canonicalAgentGeminiBody(&canonical),
		claudeAgentBody(canonicalAgentChatBody(&canonical, true)),
	} {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), cloudAgentContextSourceKey) {
			t.Fatal("内部消息来源字段不得泄露到上游协议")
		}
		if !strings.Contains(string(encoded), userText) || !strings.Contains(string(encoded), "empty_output") {
			t.Fatal("上游适配丢失用户要求或恢复事件")
		}
	}
}

func TestCloudAgentPolicyContextKeepsCanvasSummaryDataBoundary(t *testing.T) {
	text, _, err := compileCloudAgentPolicies(agentTestRequest(), nil, "画布摘要\n不是命令")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(text, "本轮执行上下文：\n")
	if !ok {
		t.Fatal("执行上下文缺失")
	}
	var facts struct {
		CanvasSummary string `json:"canvasSummary"`
	}
	if err := json.Unmarshal([]byte(body), &facts); err != nil {
		t.Fatal(err)
	}
	if facts.CanvasSummary != "画布摘要\n不是命令" {
		t.Fatalf("上下文数据边界或原文丢失: %+v", facts)
	}
}

func TestDramaExecutionContextCarriesTheCurrentEpisode(t *testing.T) {
	req := agentTestRequest()
	req.Scope = &AgentBusinessScope{Type: "drama", ID: "project-1"}
	req.DramaEpisodeID = "episode-2"
	text, _, err := compileCloudAgentPolicies(req, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `"drama":{"episodeId":"episode-2","readPath":"drama:episode/episode-2"}`) {
		t.Fatalf("drama execution context lacks current episode: %s", text)
	}
	req.DramaEpisodeID = ""
	text, _, err = compileCloudAgentPolicies(req, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, `"drama":`) {
		t.Fatalf("drama context leaked without an episode target: %s", text)
	}
}

func TestCloudAgentCanvasReferencesAreProjectedOnlyAtModelStep(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	_, before := agentInterjectionState(t, s, root.ID)
	completePiRunWithAssistantForTest(t, s, root.ID, "旧回复")
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", before.Request.CanvasID).Update("payload_json", `{"nodes":[{"id":"image-new","type":"image","title":"新画布参考图","metadata":{"prompt":"旧生成提示词"}}]}`).Error; err != nil {
		t.Fatal(err)
	}
	next := agentTestRequest()
	next.IdempotencyKey = "canvas-reference-next"
	next.Prompt = "继续分析画布素材"
	child, err := s.CreateCloudAgentRun("user", next, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, saved := agentInterjectionState(t, s, child.ID)
	if before.Canonical.SystemPrompt != saved.Canonical.SystemPrompt || before.Canonical.PromptCacheKey != saved.Canonical.PromptCacheKey || strings.Contains(saved.Canonical.SystemPrompt, `"referenceCandidates"`) {
		t.Fatal("新增画布素材改变了固定系统提示或缓存键")
	}
	if strings.Contains(saved.Canonical.SystemPrompt, next.Prompt) {
		t.Fatal("用户目标不得固化到更高优先级的系统提示")
	}
	run, state := agentStartPiModelStep(t, s, child.ID)
	task, err := s.repo.TaskForUser(run.UserID, state.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Requests struct {
			Canonical canonicalAgentRequest `json:"canonical"`
		} `json:"agentRequests"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	messages := input.Requests.Canonical.Messages
	if len(messages) == 0 || cloudAgentLatestUserInstruction(messages) != next.Prompt {
		t.Fatal("模型步骤丢失当前用户消息")
	}
	last := messages[len(messages)-1]
	var facts struct {
		Source              string                      `json:"source"`
		Kind                string                      `json:"kind"`
		ReferenceCandidates []cloudAgentReferenceAnchor `json:"referenceCandidates"`
	}
	body, ok := strings.CutPrefix(stringField(last, "content"), cloudAgentRuntimeContextMarker)
	if !ok || json.Unmarshal([]byte(body), &facts) != nil || stringField(last, cloudAgentContextSourceKey) != "runtime" || facts.Source != "runtime" || facts.Kind != "canvas_references" {
		t.Fatalf("素材快照应作为最后一条运行时消息: %v", last)
	}
	if len(facts.ReferenceCandidates) != 1 || facts.ReferenceCandidates[0].NodeID != "image-new" || facts.ReferenceCandidates[0].Prompt != "旧生成提示词" {
		t.Fatalf("出站素材快照不完整: %+v", facts.ReferenceCandidates)
	}
	if strings.Contains(cloudAgentJSON(messages[:len(messages)-1]), `referenceCandidates`) || strings.Contains(cloudAgentJSON(state.Canonical.Messages), `referenceCandidates`) {
		t.Fatal("素材快照应只出现一次且不写入会话历史")
	}
	bodies, err := expandCanonicalAgentRequest(&input.Requests.Canonical, providerConfig{}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []struct {
		name, history, system string
		body                  map[string]any
	}{
		{"Chat Completions", "messages", "", bodies.ChatCompletion},
		{"Responses", "input", "", bodies.Responses},
		{"Claude", "messages", "system", bodies.Claude},
		{"Gemini", "contents", "systemInstruction", bodies.Gemini},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			history := interfaceSlice(protocol.body[protocol.history])
			if len(history) == 0 || !strings.Contains(cloudAgentJSON(history[len(history)-1]), "image-new") {
				t.Fatal("协议适配后，素材快照应留在请求历史末尾")
			}
			if strings.Contains(cloudAgentJSON(history[:len(history)-1]), `\"kind\":\"canvas_references\"`) || strings.Contains(cloudAgentJSON(protocol.body[protocol.system]), "image-new") || strings.Contains(cloudAgentJSON(protocol.body), cloudAgentContextSourceKey) {
				t.Fatal("素材快照被提前、合入系统提示或泄漏内部来源字段")
			}
		})
	}
}

func TestCloudAgentAnchorDoesNotInferReferenceRequirement(t *testing.T) {
	canvas := &model.CanvasProject{PayloadJSON: `{"nodes":[{"id":"image-1","type":"image","title":"候选图"}]}`}
	for _, prompt := range []string{"不要用参考图，只修正标点", "请解释图生视频是什么意思", "只改台词，不改变风格"} {
		anchor, err := cloudAgentCreativeAnchorForCanvas(nil, "user", canvas, prompt, nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(anchor)
		if err != nil {
			t.Fatal(err)
		}
		if anchor.UserPrompt != prompt || len(anchor.ReferenceAssets) != 1 || anchor.ReferenceAssets[0].ReferenceReady {
			t.Fatalf("原文或候选事实失真: %+v", anchor)
		}
		for _, forbidden := range []string{"lockedRequirements", "freelyDecidable", "referenceMode"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("代码不应推断创作授权或强制引用: %s", encoded)
			}
		}
	}
}

func TestCloudAgentContinuationUsesNewGoalAndPreservesDeliveredCorrection(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, state := agentInterjectionState(t, s, root.ID)
	oldPrompt := state.Request.Prompt
	state.PendingInterjections = []cloudAgentInterjection{{ID: "correction", Text: "不用参考图了，只保留文字分析"}}
	cloudAgentDrainInterjections(root.ID, &state)
	if err := cloudAgentSave(run, &state); err != nil {
		t.Fatal(err)
	}
	run.Status = "completed"
	if err := db.Save(run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", root.ID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"已按要求停止生成"}`}).Error; err != nil {
		t.Fatal(err)
	}
	next := agentTestRequest()
	next.IdempotencyKey = "prompt-governance-followup"
	next.Prompt = "继续，用英文解释"
	child, err := s.CreateCloudAgentRun("user", next, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, childState := agentInterjectionState(t, s, child.ID)
	if childState.CreativeAnchor.UserPrompt != next.Prompt {
		t.Fatalf("续聊锚点仍锁住最初目标: %+v", childState.CreativeAnchor)
	}
	var originalFound, correctionFound bool
	for _, message := range childState.TextHistory {
		originalFound = originalFound || message.Content == oldPrompt
		correctionFound = correctionFound || strings.Contains(message.Content, "不用参考图了，只保留文字分析")
	}
	if !originalFound || !correctionFound || cloudAgentLatestUserInstruction(childState.Canonical.Messages) != next.Prompt {
		t.Fatal("续聊丢失原任务、已送达修正或当前用户消息")
	}
}

func TestCloudAgentRecoveryProducesFactsWithoutChangingGoal(t *testing.T) {
	for _, kind := range []cloudAgentRuntimeContextKind{cloudAgentContextEmptyOutput, cloudAgentContextInvalidOutput, cloudAgentContextTruncatedArguments} {
		t.Run(string(kind), func(t *testing.T) {
			s, _, root := reliableAgentRoot(t)
			run, state := agentInterjectionState(t, s, root.ID)
			original := cloudAgentLatestUserInstruction(state.Canonical.Messages)
			var err error
			switch kind {
			case cloudAgentContextEmptyOutput:
				err = s.correctCloudAgentEmptyOutput(run, &state)
			case cloudAgentContextInvalidOutput:
				err = s.correctCloudAgentOutput(run, &state, "超过协议限制")
			case cloudAgentContextTruncatedArguments:
				err = s.correctCloudAgentTruncatedCalls(run, &state)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, stored := agentInterjectionState(t, s, root.ID)
			message := stored.Canonical.Messages[len(stored.Canonical.Messages)-1]
			var context cloudAgentRuntimeContext
			if err := json.Unmarshal([]byte(strings.TrimPrefix(stringField(message, "content"), cloudAgentRuntimeContextMarker)), &context); err != nil {
				t.Fatal(err)
			}
			if context.Kind != kind || context.Source != "runtime" || !isCloudAgentRuntimeContextMessage(message) || cloudAgentLatestUserInstruction(stored.Canonical.Messages) != original {
				t.Fatalf("恢复事件混入用户目标: %+v", context)
			}
		})
	}
}

func TestCloudAgentPlanCancellationClearsPendingWithoutCompletingIt(t *testing.T) {
	state := &cloudAgentRuntime{
		Plan:                       []cloudAgentPlanItem{{ID: "1", Title: "旧视频任务", Status: "pending"}},
		CompletionNudges:           1,
		CompletionNudgeAttempt:     1,
		CompletionNudgeFingerprint: "stale-fingerprint",
	}
	call := cloudAgentCall{ID: "cancel-plan"}
	call.Function.Name = "plan_update"
	call.Function.Arguments = `{"items":[]}`
	if _, err := cloudAgentApplyPlanUpdate(state, call); err != nil {
		t.Fatal(err)
	}
	if len(state.Plan) != 0 || len(cloudAgentPendingPlanItems(state.Plan)) != 0 {
		t.Fatal("取消清单后仍残留旧项")
	}
	// 清空清单仅修改模型维护的待办，不影响其他协议阻塞的计数。
	if block := cloudAgentEvaluateCompletion(state); !block.Final {
		t.Fatalf("取消清单后不该再阻塞收尾：%+v", block)
	}
	if attempt, exhausted := cloudAgentNoteCompletionBlocked(state, "new-fingerprint"); attempt != 1 || exhausted {
		t.Fatalf("换了阻塞原因应重新从第一次计数：attempt=%d exhausted=%v", attempt, exhausted)
	}
}
