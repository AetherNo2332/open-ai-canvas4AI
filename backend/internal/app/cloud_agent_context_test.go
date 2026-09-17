package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/agentcontext"
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
	if evicted, _, _ := compactCloudAgentContext(&request); !evicted {
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
	if evicted, _, _ := compactCloudAgentContext(&request); evicted {
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
