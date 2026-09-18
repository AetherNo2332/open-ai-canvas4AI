package app

import (
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestCloudAgentContextCompactionPreservesToolPairsAndWrites(t *testing.T) {
	request := canonicalAgentRequest{SystemPrompt: "system", PromptCacheKey: "stable", Messages: []map[string]any{{"role": "user", "content": "original instructions"}}}
	write := `{"taskId":"paid-task","nodeId":"node","taskSubmitted":true,"status":"running"}`
	for i := 0; i < 16; i++ {
		body := `{"path":"SKILL.md","content":"` + strings.Repeat("x", 16000) + `"}`
		if i == 1 {
			body = write
		}
		request.Messages = append(request.Messages,
			map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": i}}},
			map[string]any{"role": "tool", "tool_call_id": i, "content": body})
	}
	last, _ := json.Marshal(request.Messages[len(request.Messages)-2:])
	if evicted, _, _ := compactCloudAgentContext(&request, nil); !evicted {
		t.Fatal("large read bodies not compacted")
	}
	if len(request.Messages) != 33 || request.Messages[0]["content"] != "original instructions" || request.Messages[4]["content"] != write {
		t.Fatal("instructions or write receipt lost")
	}
	after, _ := json.Marshal(request.Messages[len(request.Messages)-2:])
	if string(last) != string(after) || request.SystemPrompt != "system" || request.PromptCacheKey != "stable" {
		t.Fatal("latest turn or cache prefix modified")
	}
	for i := 1; i < len(request.Messages); i += 2 {
		if request.Messages[i]["role"] != "assistant" || request.Messages[i+1]["role"] != "tool" {
			t.Fatal("tool pair broken")
		}
	}
	if evicted, _, _ := compactCloudAgentContext(&request, nil); evicted {
		t.Fatal("compaction is not idempotent")
	}
}

func TestCloudAgentCanonicalUsesAutomaticToolChoice(t *testing.T) {
	req := agentTestRequest()
	request := cloudAgentCanonical("system", nil, "读取画布", req)
	if request.ToolChoice != "auto" {
		t.Fatalf("cloud agent tool choice = %#v", request.ToolChoice)
	}
}

func TestCloudAgentSemanticCompactionKeepsRecentPairs(t *testing.T) {
	messages := []map[string]any{}
	for i := 0; i < 8; i++ {
		messages = append(messages,
			map[string]any{"role": "user", "content": "用户提示-" + string(rune('A'+i))},
			map[string]any{"role": "assistant", "content": "规划回复-" + string(rune('A'+i))},
		)
	}
	state := cloudAgentRuntime{TextHistory: make([]providerTextMessage, 14), Canonical: canonicalAgentRequest{Messages: messages}}
	needed, _, turns := cloudAgentContextShouldCompact(&state)
	if !needed || turns != 8 {
		t.Fatalf("semantic compaction not scheduled: needed=%v turns=%d", needed, turns)
	}
	recent := cloudAgentRecentConversation(messages, 2)
	if len(recent) != 4 || recent[0].Content != "用户提示-G" || recent[3].Content != "规划回复-H" {
		t.Fatalf("recent pairs not retained: %+v", recent)
	}
}

func TestCloudAgentFallbackCheckpointMergesPreviousScriptPlan(t *testing.T) {
	previous := agentcontext.Checkpoint{
		Version: agentcontext.Version, HistorySummary: "用户要求三幕结构", ScriptDesign: "主角在雨夜车站发现时间循环",
		PendingTasks: []string{"task-old 运行中"}, Decisions: []string{"采用非线性叙事"}, CompactedTurnCount: 6,
	}
	framed, err := agentcontext.Frame(previous)
	if err != nil {
		t.Fatal(err)
	}
	state := cloudAgentRuntime{
		Request:        CloudAgentRequest{Prompt: "继续设计第三幕", PermissionMode: "read_only"},
		CreativeAnchor: cloudAgentCreativeAnchor{UserPrompt: "保持冷蓝色调", LockedRequirements: []string{"主角左手伤口必须连续"}},
		Canonical: canonicalAgentRequest{Messages: []map[string]any{
			{"role": "user", "content": framed},
			{"role": "assistant", "content": agentcontext.Acknowledgement},
			{"role": "user", "content": "第三幕增加一次假胜利"},
			{"role": "assistant", "content": "假胜利发生在镜头 7"},
		}},
		ContextCompaction: &cloudAgentContextCompaction{TurnCount: 7},
		Decisions:         map[string]string{"ending": "开放结局"},
	}
	checkpoint := cloudAgentFallbackCheckpoint(&state)
	checkpoint.CompactedTurnCount = state.ContextCompaction.TurnCount
	encoded, _ := json.Marshal(checkpoint)
	for _, fact := range []string{"雨夜车站", "task-old", "假胜利", "左手伤口", "开放结局"} {
		if !strings.Contains(string(encoded), fact) {
			t.Fatalf("fallback checkpoint lost %q: %s", fact, encoded)
		}
	}
	if checkpoint.CompactedTurnCount != 7 {
		t.Fatalf("turn count = %d", checkpoint.CompactedTurnCount)
	}
}

func TestCloudAgentConversationTurnCountIncludesPreviousCheckpoint(t *testing.T) {
	framed, err := agentcontext.Frame(agentcontext.Checkpoint{Version: 1, CompactedTurnCount: 9})
	if err != nil {
		t.Fatal(err)
	}
	messages := []map[string]any{{"role": "user", "content": framed}, {"role": "assistant", "content": agentcontext.Acknowledgement}, {"role": "user", "content": "继续"}}
	if count := cloudAgentConversationTurnCount(messages); count != 10 {
		t.Fatalf("turn count = %d", count)
	}
}

func TestCloudAgentPlanDoesNotBustSystemPrefix(t *testing.T) {
	state := cloudAgentRuntime{
		Canonical: canonicalAgentRequest{
			SystemPrompt:   "frozen-system",
			PromptCacheKey: "cloud-agent:abc",
			Messages:       []map[string]any{{"role": "user", "content": "做分镜"}},
		},
	}
	first := cloudAgentCanonicalWithPlan(&state)
	state.Plan = []cloudAgentPlanItem{{ID: "1", Title: "写分镜", Status: "doing"}}
	second := cloudAgentCanonicalWithPlan(&state)
	state.Plan[0].Status = "done"
	state.Plan = append(state.Plan, cloudAgentPlanItem{ID: "2", Title: "生成视频", Status: "pending"})
	third := cloudAgentCanonicalWithPlan(&state)
	if first.SystemPrompt != "frozen-system" || second.SystemPrompt != first.SystemPrompt || third.SystemPrompt != first.SystemPrompt {
		t.Fatalf("待办变化不得改写系统提示: %q / %q / %q", first.SystemPrompt, second.SystemPrompt, third.SystemPrompt)
	}
	if first.PromptCacheKey != "cloud-agent:abc" || second.PromptCacheKey != first.PromptCacheKey {
		t.Fatal("prompt cache key 应保持冻结")
	}
	if isCloudAgentRuntimeContextMessage(first.Messages[len(first.Messages)-1]) {
		t.Fatal("没有清单时不应追加运行状态")
	}
	if !strings.Contains(stringField(second.Messages[len(second.Messages)-1], "content"), "写分镜") {
		t.Fatal("清单应挂在末尾消息")
	}
	if strings.Contains(stringField(state.Canonical.Messages[len(state.Canonical.Messages)-1], "content"), "生成视频") {
		t.Fatal("运行态历史不得钉死清单快照")
	}
}

func TestCloudAgentUnlimitedBudgetKeepsGenerationTools(t *testing.T) {
	for _, limit := range []int{0, 25} {
		req := agentTestRequest()
		req.PermissionMode = "auto"
		req.Budget.MaxGenerationTasks = limit
		req.Budget.MaxVideoSeconds = limit * 100
		for i := 0; i < 12; i++ {
			req.SkillIDs = append(req.SkillIDs, strings.Repeat("s", i+1))
		}
		if err := validateCloudAgentRequest(&req); err != nil {
			t.Fatal(err)
		}
		if !cloudAgentToolAllowed(req, "generate_media") || !cloudAgentToolAllowed(req, "model_list") {
			t.Fatal("unlimited budget disabled generation")
		}
	}
	state := cloudAgentRuntime{Request: agentTestRequest(), Generations: 100, VideoSeconds: 5000}
	a := cloudAgentMediaArgs{Mode: "video", Duration: 180, Prompt: "p", Title: "title", NodeID: "new-node", SnapshotHash: strings.Repeat("a", 64), Size: "16:9"}
	if err := validateCloudAgentMediaArgs(a, &state); err != nil {
		t.Fatal(err)
	}
	state.Request.Budget.MaxVideoSeconds = 5100
	if err := validateCloudAgentMediaArgs(a, &state); err == nil {
		t.Fatal("positive video budget bypassed")
	}
	state.Request.Budget.MaxVideoSeconds = 0
	state.Request.Budget.MaxGenerationTasks = 100
	if err := validateCloudAgentMediaArgs(a, &state); err == nil {
		t.Fatal("positive generation budget bypassed")
	}
}

func TestCloudAgentSkillsLoadOnDemandAndPage(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	var ids []string
	for i := 0; i < 10; i++ {
		skill, err := s.CreateSkill("user", SkillMutationRequest{SkillName: strings.Repeat("s", i+1), Description: "test", Instruction: "# Test\n\nDescription\n\n" + strings.Repeat("中文", 10000), Tag: "others", IsPrivate: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, skill.SkillID)
	}
	snapshots, err := s.cloudAgentSkills("user", ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range snapshots {
		if skill.Instruction != "" || skill.Files[cloudAgentSkillEntryPath] != "" {
			t.Fatal("eager skill content")
		}
	}
	state := cloudAgentRuntime{Skills: snapshots}
	call := skillFeedbackCall(ids[0], cloudAgentSkillEntryPath)
	result, err := cloudAgentReadTool(nil, "user", &state, call, s)
	if err != nil {
		t.Fatal(err)
	}
	page := result.(map[string]any)
	if len([]rune(page["content"].(string))) != 12000 || page["hasMore"] != true {
		t.Fatal("missing bounded page")
	}
	args, _ := json.Marshal(map[string]any{"skillId": ids[0], "path": cloudAgentSkillEntryPath, "offset": page["nextOffset"]})
	call.Function.Arguments = string(args)
	result, err = cloudAgentReadTool(nil, "user", &state, call, s)
	if err != nil || result.(map[string]any)["hasMore"] != false {
		t.Fatalf("continuation failed: %v", err)
	}
	if _, err := cloudAgentReadTool(nil, "other-user", &cloudAgentRuntime{Skills: snapshots}, skillFeedbackCall(ids[0], cloudAgentSkillEntryPath), s); err == nil {
		t.Fatal("cross-user skill read")
	}
	snapshots[0].Hash = "changed"
	if _, err := cloudAgentReadTool(nil, "user", &cloudAgentRuntime{Skills: snapshots}, skillFeedbackCall(ids[0], cloudAgentSkillEntryPath), s); err == nil {
		t.Fatal("mixed skill version")
	}
}

// numericEquals 比较事件载荷里的数字：经过 JSON 往返后可能是 float64/int64/int。
func numericEquals(value any, want float64) bool {
	switch typed := value.(type) {
	case float64:
		return typed == want
	case int:
		return float64(typed) == want
	case int64:
		return float64(typed) == want
	default:
		return false
	}
}

// withConfiguredWindow 给测试渠道模型配上"用户填的模型上限"（contextWindowTokens − reservedOutputTokens）。
func withConfiguredWindow(t *testing.T, db *gorm.DB, contextWindow, reserved int) {
	t.Helper()
	config := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	config.Text.ContextWindowTokens = contextWindow
	config.Text.ReservedOutputTokens = reserved
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").
		Update("capability_config_json", mustEncodeModelCapabilityConfig(t, config)).Error; err != nil {
		t.Fatal(err)
	}
}

// anchoredState 造一个"下一步预计输入 token"可控的状态：锚点已采信且本地估算没变，
// 于是 projectedTokens == anchor.InputTokens，压缩比例就是可控的。
func anchoredState(t *testing.T, canonical canonicalAgentRequest, anchorInputTokens int) *cloudAgentRuntime {
	t.Helper()
	raw, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	return &cloudAgentRuntime{
		Request:   CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"},
		Canonical: canonical,
		TokenAnchor: &cloudAgentTokenAnchor{
			TaskID: "task-anchor", Step: 3, InputTokens: int64(anchorInputTokens),
			EstimatedTokens: estimateCloudAgentTokens(raw), Accepted: true, SourceBytes: len(raw),
		},
	}
}

// 压缩判据必须按"用户配置的模型上限"的 token 口径：达到 80% 才压，
// 且上下界都要卡住——低于阈值不能压，高于阈值必须压。
func TestCloudAgentCompactionTriggersAtConfiguredWindowShare(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	withConfiguredWindow(t, db, 100000, 20000) // 可用输入 80000 → 阈值 64000
	canonical := canonicalAgentRequest{
		SystemPrompt: "系统策略", Tools: []map[string]any{{"type": "function"}},
		Messages: []map[string]any{{"role": "user", "content": "按照画风整理画布"}},
	}

	below := anchoredState(t, canonical, 63000)
	needed, _, _, reading, hasReading := cloudAgentCompactionDecision(s.repo, below, canonical)
	if !hasReading || needed {
		t.Fatalf("63k/80k 不该触发压缩: needed=%v reading=%+v", needed, reading)
	}
	if reading.UsableInputTokens != 80000 || reading.TokenSource != "provider" {
		t.Fatalf("读数口径不对: %+v", reading)
	}

	above := anchoredState(t, canonical, 65000)
	needed, sourceBytes, turnCount, reading, hasReading := cloudAgentCompactionDecision(s.repo, above, canonical)
	if !hasReading || !needed {
		t.Fatalf("65k/80k（81%%）必须触发压缩: needed=%v reading=%+v", needed, reading)
	}
	if reading.Ratio < cloudAgentCompactionRatio || sourceBytes <= 0 || turnCount != 1 {
		t.Fatalf("触发读数不完整: ratio=%.3f bytes=%d turns=%d", reading.Ratio, sourceBytes, turnCount)
	}

	// 没有配置模型上限的渠道只能退回字节/条数兜底，且必须如实报告"没有 token 读数"。
	withConfiguredWindow(t, db, 0, 0)
	if _, _, _, _, ok := cloudAgentCompactionDecision(s.repo, above, canonical); ok {
		t.Fatal("没配上限时不该声称有 token 读数")
	}
}

// 兜底规则必须数"当前会话"，不是压缩后残留的 TextHistory——
// 历史实现数 TextHistory（实测最多 7 条），让"≥16 条"这条规则永远是死的。
func TestCloudAgentCompactionFallbackCountsLiveConversation(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	canonical := canonicalAgentRequest{Messages: []map[string]any{{"role": "user", "content": "开始"}}}
	for i := 0; i < 20; i++ {
		canonical.Messages = append(canonical.Messages,
			map[string]any{"role": "assistant", "content": "继续"},
			map[string]any{"role": "user", "content": "下一步"})
	}
	state := &cloudAgentRuntime{
		Request:   CloudAgentRequest{ChannelID: "channel", ChannelModelKey: "text-test"},
		Canonical: canonical,
		TextHistory: []providerTextMessage{
			{Role: "user", Content: "<agent-context-checkpoint>{}</agent-context-checkpoint>"},
			{Role: "assistant", Content: "已载入"},
		},
	}
	needed, _, turnCount, _, hasReading := cloudAgentCompactionDecision(s.repo, state, canonical)
	if hasReading {
		t.Fatal("未配置模型上限时不该有 token 读数")
	}
	if !needed {
		t.Fatal("41 条会话消息必须触发兜底压缩（旧实现数 TextHistory 只有 2 条，永远不触发）")
	}
	if turnCount != 21 {
		t.Fatalf("轮数统计不对: %d", turnCount)
	}

	short := &cloudAgentRuntime{Canonical: canonicalAgentRequest{Messages: canonical.Messages[:4]}, TextHistory: state.TextHistory}
	if needed, _, _, _, _ := cloudAgentCompactionDecision(s.repo, short, short.Canonical); needed {
		t.Fatal("短会话不该触发压缩")
	}
}

// 达到阈值时要【暂停步进循环】：先把历史压成检查点，压完用 Resume 继续本轮，
// 而不是把本轮判完成、也不是带着超高占用再发一次请求。
func TestCloudAgentMidRunCompactionPausesAndResumes(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	withConfiguredWindow(t, db, 100000, 20000)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	// 中途评估发生在模型响应回来之后：那一刻 ActiveTaskID 已被清空。
	state.ActiveTaskID = ""
	state.Request.ChannelID = "channel"
	state.Request.ChannelModelKey = "text-test"
	state.Canonical = canonicalAgentRequest{
		SystemPrompt: "系统策略",
		Messages:     []map[string]any{{"role": "user", "content": "按照画风整理画布"}},
	}
	raw, _ := json.Marshal(state.Canonical)
	state.TokenAnchor = &cloudAgentTokenAnchor{TaskID: "anchor", Step: 4, InputTokens: 70000, EstimatedTokens: estimateCloudAgentTokens(raw), Accepted: true, SourceBytes: len(raw)}

	requested, err := s.cloudAgentRequestCompaction(run, &state, state.Canonical)
	if err != nil || !requested {
		t.Fatalf("70k/80k 应请求压缩: requested=%v err=%v", requested, err)
	}
	if state.ContextCompaction == nil || !state.ContextCompaction.Resume || state.ContextCompaction.Status != "requested" {
		t.Fatalf("压缩请求状态不对: %+v", state.ContextCompaction)
	}
	persisted, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := cloudAgentDecode(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ContextCompaction == nil || stored.ContextCompaction.Status != "requested" {
		t.Fatalf("压缩请求没有落库: %+v", stored.ContextCompaction)
	}
	found := false
	for _, event := range stored.Events {
		if event.Type != "context_compaction_requested" {
			continue
		}
		found = true
		if event.Payload["basis"] != "tokens" || event.Payload["thresholdRatio"] != cloudAgentCompactionRatio {
			t.Fatalf("触发事件口径不对: %+v", event.Payload)
		}
		if !numericEquals(event.Payload["projectedTokens"], 70000) || !numericEquals(event.Payload["usableInputTokens"], 80000) {
			t.Fatalf("触发事件缺读数: %+v", event.Payload)
		}
	}
	if !found {
		t.Fatal("缺少 context_compaction_requested 事件")
	}

	// 压缩任务成功后：留下检查点、保持运行（不判完成），并允许本轮继续。
	checkpoint := agentcontext.Checkpoint{Version: agentcontext.Version, HistorySummary: "整理过画风分组", CompactedTurnCount: 1}
	encoded, _ := json.Marshal(checkpoint)
	result, _ := json.Marshal(map[string]string{"text": string(encoded)})
	if err := db.Model(&model.Task{}).Where("id = ?", root.ID).
		Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": string(result)}).Error; err != nil {
		t.Fatal(err)
	}
	task, err := s.repo.TaskForUser("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state.ContextCompaction.Status = "running"
	state.ActiveTaskID = root.ID
	// 请求压缩已经推进过 revision，必须重新读一次再落压缩结果。
	run, err = s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.advanceCloudAgentContextCompaction(run, &state, task); err != nil {
		t.Fatal(err)
	}
	after, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	final, err := cloudAgentDecode(after)
	if err != nil {
		t.Fatal(err)
	}
	if final.ContextCompaction != nil || final.ContextCheckpoint == nil {
		t.Fatalf("检查点没有留下或压缩态没清掉: compaction=%+v checkpoint=%v", final.ContextCompaction, final.ContextCheckpoint != nil)
	}
	if final.ContextCompactionCount != 1 {
		t.Fatalf("压缩次数没有累加: %d", final.ContextCompactionCount)
	}
	if after.Status != "running" {
		t.Fatalf("中途压缩后本轮必须继续（status=running），实际 %s", after.Status)
	}
	resumed := false
	for _, event := range final.Events {
		if event.Type == "context_compacted" && event.Payload["resume"] == true {
			resumed = true
		}
	}
	if !resumed {
		t.Fatal("context_compacted 事件没有标明 resume")
	}
}

// 正文卸载必须留痕：这条路径过去是静默的，实测全库 context_evicted 事件数为 0，
// 于是"到底卸载过没有"在运行记录里查不到。
func TestCloudAgentSaveEmitsEvictionEventOnce(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": "指令"}}
	for i := 0; i < 26; i++ {
		state.Canonical.Messages = append(state.Canonical.Messages,
			map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": i}}},
			map[string]any{"role": "tool", "tool_call_id": i, "content": `{"content":"` + strings.Repeat("x", 700) + `"}`})
	}
	save := func() {
		current, err := s.repo.CloudAgent("user", root.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.repo.MutateCloudAgent("user", root.ID, current.Revision, func(execution *model.CloudAgentExecution, _ *repository.Repository) error {
			return cloudAgentSave(execution, &state)
		}); err != nil {
			t.Fatal(err)
		}
	}
	save()
	after, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	countEvicted := func(execution *model.CloudAgentExecution) int {
		decoded, err := cloudAgentDecode(execution)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, event := range decoded.Events {
			if event.Type == "context_evicted" {
				count++
			}
		}
		return count
	}
	if got := countEvicted(after); got != 1 {
		t.Fatalf("卸载事件应当只发一次，实际 %d", got)
	}
	save()
	again, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := countEvicted(again); got != 1 {
		t.Fatalf("重复保存不该重复发事件，实际 %d", got)
	}
	_ = db
}
