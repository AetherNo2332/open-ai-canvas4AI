package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 事件全量落在上游的 cloud_agent_event_records（EventJSON 整条 JSON，主键 run_id+sequence），
// 检查点只留控制面；内存里只保留最近一窗，运行详情与 SSE 按 seq 分页/增量读表。
//
// 本用例守住三件事：
//   - 保存仍然与检查点同事务落库，且检查点里不再带消息与事件；
//   - 解码只载入一窗，EventSeqBase 落在窗口之前，窗口最后一条等于 EventCount；
//   - 默认视图返回最近一页、sinceSeq 返回增量、跨窗口边界序号连续。
func TestCloudAgentEventsPersistWithCheckpointAndPage(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	// 事件数特意超过默认页大小，才能同时验证"事件窗口"与"默认页从表里补齐"。
	state.Events = nil
	state.EventSeqBase = 0
	total := cloudAgentRunEventPageLimit + 25
	for index := 0; index < total; index++ {
		state.event(run.ID, "tool_completed", map[string]any{"toolName": "canvas_get_state", "text": strings.Repeat("读", 40)})
	}
	if len(state.Events) != total {
		t.Fatalf("前置事件数不对：%d", len(state.Events))
	}
	// 走真实保存路径：journal 挂到 run 上，由 MutateCloudAgent 的事务与检查点一起写库。
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.CloudAgentEventRecordCount("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if int(stored) != total {
		t.Fatalf("入库条数 = %d，期望 %d", stored, total)
	}
	// 检查点里不再有消息与事件。
	persisted, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.CheckpointVersion != cloudAgentCheckpointVersion {
		t.Fatalf("检查点版本 = %d，期望 %d", persisted.CheckpointVersion, cloudAgentCheckpointVersion)
	}
	if persisted.EventCount != total {
		t.Fatalf("事件水位 = %d，期望 %d", persisted.EventCount, total)
	}
	checkpoint := map[string]any{}
	if err := json.Unmarshal([]byte(persisted.StateJSON), &checkpoint); err != nil {
		t.Fatalf("检查点不是合法 JSON: %v", err)
	}
	if checkpoint["events"] != nil || checkpoint["textHistory"] != nil {
		t.Fatalf("检查点里仍带着事件或历史：%s", truncateRunes(persisted.StateJSON, 200))
	}
	if canonical, ok := checkpoint["canonical"].(map[string]any); !ok || canonical["messages"] != nil {
		t.Fatalf("检查点里仍带着会话消息：%s", truncateRunes(persisted.StateJSON, 200))
	}
	// 重建：消息来自 transcript，事件只载入一窗，水位落在窗口之前。
	rebuilt, err := cloudAgentDecode(persisted)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if len(rebuilt.Events) != repository.CloudAgentJournalWindow {
		t.Fatalf("事件窗口 = %d 条，期望 %d", len(rebuilt.Events), repository.CloudAgentJournalWindow)
	}
	if rebuilt.EventSeqBase != total-repository.CloudAgentJournalWindow {
		t.Fatalf("事件水位 = %d，期望 %d", rebuilt.EventSeqBase, total-repository.CloudAgentJournalWindow)
	}
	if rebuilt.Events[len(rebuilt.Events)-1].Seq != total {
		t.Fatalf("窗口末尾序号 = %d，期望 %d", rebuilt.Events[len(rebuilt.Events)-1].Seq, total)
	}
	if err := validateCloudAgentRuntime(persisted, &rebuilt); err != nil {
		t.Fatalf("重建后校验失败: %v", err)
	}

	// 幂等：没有新事件时再保存一次不应重复入库、也不应改动窗口。
	if err := s.repo.MutateCloudAgent("user", root.ID, persisted.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &rebuilt)
	}); err != nil {
		t.Fatal(err)
	}
	storedAgain, _ := s.repo.CloudAgentEventRecordCount("user", root.ID)
	if storedAgain != stored {
		t.Fatalf("重复保存不幂等：stored %d → %d", stored, storedAgain)
	}

	// 追加：窗口左移之后新事件仍然接在水位之后，历史不被截断。
	appended, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	grown, err := cloudAgentDecode(appended)
	if err != nil {
		t.Fatal(err)
	}
	grown.event(root.ID, "assistant_message", map[string]any{"text": "追加一条"})
	if err := s.repo.MutateCloudAgent("user", root.ID, appended.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &grown)
	}); err != nil {
		t.Fatal(err)
	}
	grownCount, _ := s.repo.CloudAgentEventRecordCount("user", root.ID)
	if int(grownCount) != total+1 {
		t.Fatalf("追加后入库条数 = %d，期望 %d", grownCount, total+1)
	}

	// 默认视图：返回最近一页（事件窗口 + 表里更早的部分），序号连续。
	latest, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	viewState, err := cloudAgentDecode(latest)
	if err != nil {
		t.Fatal(err)
	}
	events := s.cloudAgentRunEventsForView("user", latest, &viewState, 0, 0)
	if len(events) != cloudAgentRunEventPageLimit {
		t.Fatalf("默认页大小 = %d，期望 %d", len(events), cloudAgentRunEventPageLimit)
	}
	if events[len(events)-1].Seq != total+1 || events[0].Seq != total+1-cloudAgentRunEventPageLimit+1 {
		t.Fatalf("默认页序号不连续：首 %d 尾 %d", events[0].Seq, events[len(events)-1].Seq)
	}
	if count := s.cloudAgentRunEventCount("user", latest, &viewState); count != total+1 {
		t.Fatalf("累计条数 = %d，期望 %d", count, total+1)
	}
	// eventLimit 必须是上限而不是忽略：500 以内按传入值取。
	if narrowed := s.cloudAgentRunEventsForView("user", latest, &viewState, 0, 10); len(narrowed) != 10 {
		t.Fatalf("eventLimit=10 返回 %d 条", len(narrowed))
	}

	// 增量：sinceSeq 之后只返回更新的部分（SSE 断线重连口径）。
	delta := s.cloudAgentRunEventsForView("user", latest, &viewState, total, 0)
	if len(delta) != 1 || delta[0].Seq != total+1 {
		t.Fatalf("增量视图不对：%+v", delta)
	}
	// 跨窗口边界：从水位之前 2 条开始，应当由"表 + 窗口"共同补齐且序号连续。
	delta = s.cloudAgentRunEventsForView("user", latest, &viewState, viewState.EventSeqBase-2, 0)
	if len(delta) != 2+len(viewState.Events) {
		t.Fatalf("跨边界增量条数 = %d，期望 %d", len(delta), 2+len(viewState.Events))
	}
	for index := 1; index < len(delta); index++ {
		if delta[index].Seq != delta[index-1].Seq+1 {
			t.Fatalf("增量序号不连续：%d → %d", delta[index-1].Seq, delta[index].Seq)
		}
	}
}

// 表里没有记录（还没保存过的内存态）时，运行详情仍要能读到内存窗口里的事件。
func TestCloudAgentRunEventsFallBackToMemoryWindow(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Events = nil
	state.EventSeqBase = 0
	for index := 0; index < 5; index++ {
		state.event(run.ID, "assistant_message", map[string]any{"text": "legacy"})
	}
	events := s.cloudAgentRunEventsForView("user", run, &state, 0, 0)
	if len(events) < 5 || events[len(events)-5].Seq != 1 {
		t.Fatalf("内存窗口回退失败：%+v", events)
	}
	delta := s.cloudAgentRunEventsForView("user", run, &state, state.Events[1].Seq, 0)
	if len(delta) != 3 || delta[0].Seq != 3 {
		t.Fatalf("内存窗口增量回退失败：%+v", delta)
	}
}

// 单条事件载荷封顶 128 KiB：超限压成"需要刷新"回执，而不是让整轮判死。
func TestCloudAgentEventPayloadIsBounded(t *testing.T) {
	payload := map[string]any{"text": strings.Repeat("画", 60_000), "callId": "call-1", "operation": "canvas_apply_ops"}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= cloudAgentEventPayloadLimitBytes {
		t.Fatalf("前置载荷不够大：%d", len(raw))
	}
	bounded := cloudAgentBoundEventPayload(payload)
	encoded, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > cloudAgentEventPayloadLimitBytes {
		t.Fatalf("封顶后仍然超限：%d", len(encoded))
	}
	if bounded["requiresRefresh"] != true {
		t.Fatalf("缺少 requiresRefresh 标记：%+v", bounded)
	}
	if bounded["callId"] != "call-1" || bounded["operation"] != "canvas_apply_ops" {
		t.Fatalf("可读回执字段被丢掉：%+v", bounded)
	}
	if bounded["payloadSlimmedBytes"] != len(raw) {
		t.Fatalf("原始体积没有留痕：%+v", bounded["payloadSlimmedBytes"])
	}
	// 未超限的载荷必须原样返回（不做无谓改写）。
	small := map[string]any{"text": "ok"}
	if got := cloudAgentBoundEventPayload(small); got["text"] != "ok" || got["requiresRefresh"] != nil {
		t.Fatalf("小载荷被改写：%+v", got)
	}
}

// 事件窗口右移（长跑 run）之后，库里更早的事件仍然可以按 seq 读出来。
func TestCloudAgentEventWindowKeepsOlderRecordsReadable(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Events = nil
	state.EventSeqBase = 0
	total := repository.CloudAgentJournalWindow * 3
	for index := 0; index < total; index++ {
		state.event(run.ID, "tool_completed", map[string]any{"toolName": "canvas_get_state", "step": index + 1})
	}
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	persisted, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := cloudAgentDecode(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilt.Events) != repository.CloudAgentJournalWindow {
		t.Fatalf("窗口 = %d，期望 %d", len(rebuilt.Events), repository.CloudAgentJournalWindow)
	}
	// eventLimit 超过窗口：必须从事件表往前补齐，而不是只给窗口里那 40 条。
	page := s.cloudAgentRunEventsForView("user", persisted, &rebuilt, 0, repository.CloudAgentJournalWindow+10)
	if len(page) != repository.CloudAgentJournalWindow+10 {
		t.Fatalf("补齐后页大小 = %d，期望 %d", len(page), repository.CloudAgentJournalWindow+10)
	}
	if page[len(page)-1].Seq != total || page[0].Seq != total-repository.CloudAgentJournalWindow-10+1 {
		t.Fatalf("补齐页序号不连续：首 %d 尾 %d", page[0].Seq, page[len(page)-1].Seq)
	}
}
