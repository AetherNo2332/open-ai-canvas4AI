package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
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
	// 注意：创建运行时已经落过事件（context_pressure），journal 是 append-only，
	// 因此这里只在尾部追加，不重置已入库的序号与内容。
	total := cloudAgentRunEventPageLimit + 25
	for len(state.Events) < total {
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

// 运行详情的事件分页契约。事件全量在 cloud_agent_event_records（一行一条 EventJSON，
// 主键 run_id + sequence），内存只保留最近一窗，因此读路径必须自己说明"这一页在整条
// 日志里的位置"。本用例守住四件事：
//   - 载入（hydrate + decode）只取一窗，水位落在窗口之前，窗口末条等于 EventCount；
//   - 默认视图是尾部一窗，events[i].Seq == EventSeqBase + i + 1 成立；
//   - sinceSeq 只返回增量，eventLimit 决定页大小（大于窗口时从事件表往前补）；
//   - eventCount / latestSeq / eventsTruncated 三个字段互相自洽。
func TestCloudAgentRunDetailPagesJournalBySeq(t *testing.T) {
	s, _, root := reliableAgentRoot(t)
	// 事件数刻意跨过内存窗口与 HTTP 单次上限，才能同时验证"尾部窗口"、"从表里往前补页"
	// 和"内部调用方（续轮收束）可以要更大的页"三条路径。
	total := cloudAgentRunEventDeltaLimit + repository.CloudAgentJournalWindow*2
	growAgentJournal(t, s, root.ID, total)
	stored, err := s.repo.CloudAgentEventRecordCount("user", root.ID)
	if err != nil || int(stored) != total {
		t.Fatalf("入库条数 = %d（err=%v），期望 %d", stored, err, total)
	}

	persisted, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.EventCount != total {
		t.Fatalf("事件水位 = %d，期望 %d", persisted.EventCount, total)
	}
	rebuilt, err := cloudAgentDecode(persisted)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if len(rebuilt.Events) != repository.CloudAgentJournalWindow {
		t.Fatalf("载入窗口 = %d 条，期望 %d", len(rebuilt.Events), repository.CloudAgentJournalWindow)
	}
	if rebuilt.EventSeqBase != total-repository.CloudAgentJournalWindow {
		t.Fatalf("窗口水位 = %d，期望 %d", rebuilt.EventSeqBase, total-repository.CloudAgentJournalWindow)
	}
	if rebuilt.Events[len(rebuilt.Events)-1].Seq != total {
		t.Fatalf("窗口末条序号 = %d，期望 %d", rebuilt.Events[len(rebuilt.Events)-1].Seq, total)
	}

	// 默认视图：最近一页（默认页大于内存窗口时由事件表往前补齐），
	// 并且如实报告"还有更早的记录"。
	view, err := s.CloudAgentRun("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, view, total-cloudAgentRunEventPageLimit, total, true)
	if len(view.Events) != cloudAgentRunEventPageLimit {
		t.Fatalf("默认页 = %d 条，期望 %d", len(view.Events), cloudAgentRunEventPageLimit)
	}

	// eventLimit 是页大小而不是忽略：小于窗口时只返回尾部这么多条。
	narrowed, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{EventLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, narrowed, total-10, total, true)
	if len(narrowed.Events) != 10 {
		t.Fatalf("eventLimit=10 返回 %d 条", len(narrowed.Events))
	}

	// eventLimit 大于窗口：必须从事件表往前补齐，而不是只给窗口里那 40 条。
	deeper, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{EventLimit: repository.CloudAgentJournalWindow + 10})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, deeper, total-repository.CloudAgentJournalWindow-10, total, true)
	if len(deeper.Events) != repository.CloudAgentJournalWindow+10 {
		t.Fatalf("补齐后页大小 = %d，期望 %d", len(deeper.Events), repository.CloudAgentJournalWindow+10)
	}

	// HTTP 单次上限（500）刚好覆盖不到整条日志：页对齐到 500，并如实标记还有更早记录。
	capped, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{EventLimit: cloudAgentRunEventDeltaLimit})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, capped, total-cloudAgentRunEventDeltaLimit, total, true)
	if len(capped.Events) != cloudAgentRunEventDeltaLimit {
		t.Fatalf("上限页 = %d 条，期望 %d", len(capped.Events), cloudAgentRunEventDeltaLimit)
	}

	// 内部调用方（续轮收束）不受 HTTP 上限约束：更大的页必须能拿到整条日志，
	// 否则交接帧会因为窗口而漏掉上一轮的真实改动。
	full, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{EventLimit: cloudAgentContinuationEventLimit})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, full, 0, total, false)
	if len(full.Events) != total {
		t.Fatalf("全量页 = %d 条，期望 %d", len(full.Events), total)
	}

	// 增量：sinceSeq 之后只返回更新的部分，且不再声称截断（客户端自己拿着游标）。
	delta, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{SinceSeq: total - 2})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, delta, total-2, total, false)
	if len(delta.Events) != 2 || delta.Events[0].Seq != total-1 {
		t.Fatalf("增量视图不对：%+v", delta.Events)
	}

	// 增量页也必须尊重 eventLimit；不能先取满默认上限，再把内存尾窗追加到页尾。
	limitedDelta, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{SinceSeq: 1, EventLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(limitedDelta.Events) != 10 || limitedDelta.Events[0].Seq != 2 || limitedDelta.Events[9].Seq != 11 {
		t.Fatalf("受限增量页不连续或超出上限：%+v", limitedDelta.Events)
	}

	// 跨窗口边界：游标落在窗口之前，表与窗口要共同补齐且序号连续。
	crossing, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{SinceSeq: rebuilt.EventSeqBase - 2})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, crossing, rebuilt.EventSeqBase-2, total, false)
	if len(crossing.Events) != repository.CloudAgentJournalWindow+2 || crossing.Events[0].Seq != rebuilt.EventSeqBase-1 {
		t.Fatalf("跨边界增量不对：%d 条，首条 %d", len(crossing.Events), crossing.Events[0].Seq)
	}

	// 游标已经追平（甚至越界）：返回空页而不是报错，客户端据此停止轮询。
	caughtUp, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{SinceSeq: total})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, caughtUp, total-repository.CloudAgentJournalWindow, total, false)
	if len(caughtUp.Events) != 0 || caughtUp.LatestSeq != 0 {
		t.Fatalf("追平后仍返回事件：%+v", caughtUp.Events)
	}
	beyond, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{SinceSeq: total + 100})
	if err != nil || len(beyond.Events) != 0 {
		t.Fatalf("越界游标应返回空页：events=%d err=%v", len(beyond.Events), err)
	}

	// 追加一条：增量视图只看得到它，latestSeq 就是下一次的游标。
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
	next, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{SinceSeq: total})
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, next, total, total+1, false)
	if len(next.Events) != 1 || next.Events[0].Seq != total+1 {
		t.Fatalf("追加后的增量不对：%+v", next.Events)
	}
}

func TestCloudAgentRunPaginationFieldsAreStableJSON(t *testing.T) {
	raw, err := json.Marshal(CloudAgentRun{})
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"eventSeqBase", "eventCount", "latestSeq", "eventsTruncated"} {
		if _, ok := encoded[field]; !ok {
			t.Fatalf("分页字段 %q 在零值响应中缺失：%s", field, raw)
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
	base := state.EventSeqBase
	for index := 0; index < 5; index++ {
		state.event(run.ID, "assistant_message", map[string]any{"text": "legacy"})
	}
	_ = base
	// 表里一条都没有：视图只能回退到内存窗口。
	events := s.cloudAgentRunEventsForView("user", run, &state, 0, 0)
	if len(events) != len(state.Events) {
		t.Fatalf("内存窗口回退失败：%+v", events)
	}
	cursor := state.Events[1].Seq
	delta := s.cloudAgentRunEventsForView("user", run, &state, cursor, 0)
	if len(delta) != len(state.Events)-2 || delta[0].Seq != cursor+1 {
		t.Fatalf("内存窗口增量回退失败：cursor=%d %+v", cursor, delta)
	}
}

// growAgentJournal 按真实保存路径把事件追加到 total 条。一次转移不可能产生几百条事件
// （超过 cloudAgentEventWindowSanityLimit 会被判"窗口异常"），因此分多轮追加：每轮重新
// 载入（窗口右移）后再写，顺带覆盖"窗口左移之后继续追加，历史不被截断"。
func growAgentJournal(t *testing.T, s *Service, runID string, total int) {
	t.Helper()
	for {
		run, err := s.repo.CloudAgent("user", runID)
		if err != nil {
			t.Fatal(err)
		}
		state, err := cloudAgentDecode(run)
		if err != nil {
			t.Fatal(err)
		}
		missing := total - (state.EventSeqBase + len(state.Events))
		if missing <= 0 {
			return
		}
		for index := 0; index < min(missing, cloudAgentEventWindowSanityLimit/2); index++ {
			state.event(runID, "tool_completed", map[string]any{"toolName": "canvas_get_state", "text": strings.Repeat("读", 20)})
		}
		if err := s.repo.MutateCloudAgent("user", runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			return cloudAgentSave(current, &state)
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// assertAgentEventPage 校验分页契约本身：条数、水位、序号不变量、latestSeq 与截断标记。
func assertAgentEventPage(t *testing.T, run *CloudAgentRun, wantBase, wantCount int, wantTruncated bool) {
	t.Helper()
	if run.EventSeqBase != wantBase {
		t.Fatalf("eventSeqBase = %d，期望 %d", run.EventSeqBase, wantBase)
	}
	if run.EventCount != wantCount {
		t.Fatalf("eventCount = %d，期望 %d", run.EventCount, wantCount)
	}
	if run.EventsTruncated != wantTruncated {
		t.Fatalf("eventsTruncated = %v，期望 %v", run.EventsTruncated, wantTruncated)
	}
	for index, event := range run.Events {
		if event.Seq != run.EventSeqBase+index+1 {
			t.Fatalf("分页不变量被破坏：events[%d].seq = %d，期望 eventSeqBase(%d)+%d+1", index, event.Seq, run.EventSeqBase, index)
		}
	}
	if len(run.Events) > 0 && run.LatestSeq != run.Events[len(run.Events)-1].Seq {
		t.Fatalf("latestSeq = %d，期望末条 %d", run.LatestSeq, run.Events[len(run.Events)-1].Seq)
	}
	// 截断标记只表示"水位之前还有记录"，不能凭空出现。
	if run.EventsTruncated && run.EventSeqBase <= 0 {
		t.Fatalf("eventsTruncated 与 eventSeqBase 不自洽：base=%d", run.EventSeqBase)
	}
}

// 事件表里没有该运行的行时（升级前的旧检查点把事件放在 state_json 里），运行详情必须
// 仍能读到这些事件，且水位为 0（它们就是全部）。
func TestCloudAgentRunDetailFallsBackToLegacyStateEvents(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		state.event(root.ID, "assistant_message", map[string]any{"text": "legacy"})
	}
	legacyJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	// 还原成升级前的形态：事件在 state_json 里、事件表为空、水位为 0。
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("run_id = ?", root.ID).Delete(&model.CloudAgentEventRecord{}).Error; err != nil {
			return err
		}
		return tx.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Updates(map[string]any{
			"checkpoint_version": 0, "event_count": 0, "message_count": 0, "state_json": string(legacyJSON),
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	view, err := s.CloudAgentRun("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentEventPage(t, view, 0, len(state.Events), false)
	if len(view.Events) != len(state.Events) {
		t.Fatalf("旧检查点事件回退失败：%d 条，期望 %d", len(view.Events), len(state.Events))
	}
	// 增量也要能从内存窗口过滤出来（表里没有行）。
	cursor := view.Events[1].Seq
	delta, err := s.CloudAgentRun("user", root.ID, CloudAgentRunViewOptions{SinceSeq: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Events) != len(state.Events)-2 || delta.Events[0].Seq != cursor+1 {
		t.Fatalf("旧检查点增量回退失败：cursor=%d events=%+v", cursor, delta.Events)
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
	total := repository.CloudAgentJournalWindow * 3
	for len(state.Events) < total {
		state.event(run.ID, "tool_completed", map[string]any{"toolName": "canvas_get_state", "step": len(state.Events) + 1})
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

// 事件行缺失但水位不为 0：不能把"一行都没读到"当成空日志，否则运行详情会给出
// 一个水位虚高、事件却为空的页，客户端会以为历史已经被清理。
func TestCloudAgentDecodeRejectsMissingJournalRows(t *testing.T) {
	s, db, root := reliableAgentRoot(t)
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.event(root.ID, "tool_completed", map[string]any{"toolName": "canvas_get_state"})
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("run_id = ?", root.ID).Delete(&model.CloudAgentEventRecord{}).Error; err != nil {
		t.Fatal(err)
	}
	orphaned, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cloudAgentDecode(orphaned); err == nil || !strings.Contains(err.Error(), "journal is incomplete") {
		t.Fatalf("事件行全缺时仍被接受：%v", err)
	}
}
