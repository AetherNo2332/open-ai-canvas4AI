package app

import (
	"encoding/json"
	"fmt"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 共同编辑：画布被他人改动（stale_snapshot）或节点正被用户编辑（node_locked）是环境状态，
// 不是模型的参数错。只要模型每次都重读过画布，就不进入纠错梯度、不收紧工具、不判死整轮。

func conflictCall(id string) cloudAgentCall {
	call := cloudAgentCall{ID: id}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = `{"snapshotHash":"old","ops":[{"type":"update_node","id":"n1","patch":{"title":"t"}}]}`
	return call
}

func conflictReread(run *model.CloudAgentExecution, state *cloudAgentRuntime, id string) {
	read := cloudAgentCall{ID: id}
	read.Function.Name = "canvas_get_state"
	read.Function.Arguments = `{}`
	cloudAgentRecordToolResult(run, state, read, map[string]any{}, nil)
}

func staleSnapshotErr() error {
	return &cloudAgentFieldArgumentError{
		error: &cloudAgentArgumentError{cloudAgentSnapshotConflictError("画布已变化，本次未写入；请重新读取")},
		Field: "snapshotHash", Issue: "stale_snapshot",
	}
}

func nodeLockedErr() error {
	return &repository.CanvasEditLeaseError{Reason: "node_locked", CanvasID: "canvas", NodeID: "n1"}
}

func lastModelReceipt(t *testing.T, state *cloudAgentRuntime) map[string]any {
	t.Helper()
	var receipt map[string]any
	message := state.Canonical.Messages[len(state.Canonical.Messages)-1]
	if err := json.Unmarshal([]byte(message["content"].(string)), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func countEvents(state *cloudAgentRuntime, kind string) int {
	count := 0
	for _, event := range state.Events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func assertConflictNotRepair(t *testing.T, newErr func() error) {
	t.Helper()
	state := &cloudAgentRuntime{}
	run := &model.CloudAgentExecution{ID: "run", Status: "running"}
	for attempt := 1; attempt <= 3; attempt++ {
		cloudAgentRecordToolResult(run, state, conflictCall(fmt.Sprintf("write-%d", attempt)), nil, newErr())
		conflictReread(run, state, fmt.Sprintf("read-%d", attempt))
	}
	if run.Status == "failed" {
		t.Fatalf("环境冲突不应判死整轮：%s", run.FailureMessage)
	}
	if _, ok := state.ToolRepairs["canvas_apply_ops"]; ok {
		t.Fatalf("环境冲突不应进入纠错计数：%+v", state.ToolRepairs)
	}
	if len(state.ToolScope) != 0 {
		t.Fatalf("环境冲突不应收紧工具集：%+v", state.ToolScope)
	}
	if got := state.EnvironmentConflicts["canvas_apply_ops"].Attempt; got != 3 {
		t.Fatalf("环境冲突应单独计数 3 次，实际 %d", got)
	}
}

func TestEnvironmentConflictDoesNotCountAsRepair(t *testing.T) {
	assertConflictNotRepair(t, staleSnapshotErr)
	// 上游只带结构化字段（没有 errCloudAgentSnapshotConflict）的过期快照同样是环境冲突。
	assertConflictNotRepair(t, func() error {
		return cloudAgentFieldError("snapshotHash", "stale_snapshot", "画布已变化")
	})
	// 画布命令的实体版本冲突。
	assertConflictNotRepair(t, func() error {
		return cloudAgentFieldError("node:n1", "entity_version_conflict", "目标实体已被其他操作修改")
	})
}

func TestNodeLockedDoesNotCountAsRepair(t *testing.T) {
	assertConflictNotRepair(t, nodeLockedErr)
}

func TestEnvironmentConflictWithoutRereadCountsAsModelError(t *testing.T) {
	for name, newErr := range map[string]func() error{"stale_snapshot": staleSnapshotErr, "node_locked": nodeLockedErr} {
		t.Run(name, func(t *testing.T) {
			state := &cloudAgentRuntime{}
			run := &model.CloudAgentExecution{ID: "run", Status: "running"}
			cloudAgentRecordToolResult(run, state, conflictCall("write-1"), nil, newErr())
			if len(state.ToolScope) != 0 || len(state.ToolRepairs) != 0 {
				t.Fatalf("第一次冲突不应计入纠错：%+v %+v", state.ToolRepairs, state.ToolScope)
			}
			// 没有重读就原样重复提交：按模型的错处理，走现有梯度第 2 次收紧。
			cloudAgentRecordToolResult(run, state, conflictCall("write-2"), nil, newErr())
			if repair := state.ToolRepairs["canvas_apply_ops"]; repair.Attempt != 2 {
				t.Fatalf("未重读的重复提交应按第 2 次纠错计数：%+v", repair)
			}
			if len(state.ToolScope) == 0 {
				t.Fatal("未重读的重复提交应进入收紧档")
			}
			retry, _ := state.Events[len(state.Events)-1].Payload["retry"].(map[string]any)
			if retry["status"] != "retrying" || retry["ladder"] != "scoped" {
				t.Fatalf("应沿用现有纠错回执：%+v", retry)
			}
			cloudAgentRecordToolResult(run, state, conflictCall("write-3"), nil, newErr())
			if run.Status != "failed" {
				t.Fatal("未重读的第三次重复提交应按现有梯度判失败")
			}
		})
	}
}

func TestEnvironmentConflictAsksUserAfterFive(t *testing.T) {
	state := &cloudAgentRuntime{}
	run := &model.CloudAgentExecution{ID: "run", Status: "running"}
	for attempt := 1; attempt <= cloudAgentEnvironmentConflictLimit; attempt++ {
		if attempt > 1 {
			conflictReread(run, state, fmt.Sprintf("read-%d", attempt))
		}
		cloudAgentRecordToolResult(run, state, conflictCall(fmt.Sprintf("write-%d", attempt)), nil, staleSnapshotErr())
		receipt := lastModelReceipt(t, state)
		if attempt < cloudAgentEnvironmentConflictLimit {
			if receipt["requiredAction"] == "ask_user" || countEvents(state, "canvas_contention") != 0 {
				t.Fatalf("第 %d 次冲突不应要求询问用户：%+v", attempt, receipt)
			}
			continue
		}
		if receipt["requiredAction"] != "ask_user" || receipt["message"] != "画布一直在变化，我先暂停：要等你编辑完再继续吗？" {
			t.Fatalf("第 5 次冲突应要求询问用户：%+v", receipt)
		}
	}
	if cloudAgentEnvironmentConflictLimit != 5 {
		t.Fatalf("连续冲突上限应为 5，实际 %d", cloudAgentEnvironmentConflictLimit)
	}
	if run.Status == "failed" {
		t.Fatalf("持续冲突不应判失败：%s", run.FailureMessage)
	}
	if countEvents(state, "canvas_contention") != 1 {
		t.Fatalf("canvas_contention 应出现一次，实际 %d", countEvents(state, "canvas_contention"))
	}
	var last CloudAgentEvent
	for _, event := range state.Events {
		if event.Type == "canvas_contention" {
			last = event
		}
	}
	if last.Payload["toolName"] != "canvas_apply_ops" || last.Payload["attempts"] != cloudAgentEnvironmentConflictLimit {
		t.Fatalf("canvas_contention 载荷不对：%+v", last)
	}
	if err := validateCloudAgentRuntimeEnvironmentConflicts(state); err != nil {
		t.Fatalf("到上限的冲突计数应可持久化：%v", err)
	}
}

func TestEnvironmentConflictResetsOnSuccess(t *testing.T) {
	state := &cloudAgentRuntime{}
	run := &model.CloudAgentExecution{ID: "run", Status: "running"}
	cloudAgentRecordToolResult(run, state, conflictCall("write-1"), nil, nodeLockedErr())
	conflictReread(run, state, "read-1")
	cloudAgentRecordToolResult(run, state, conflictCall("write-2"), nil, staleSnapshotErr())
	if state.EnvironmentConflicts["canvas_apply_ops"].Attempt != 2 {
		t.Fatalf("前置条件：应已记 2 次冲突：%+v", state.EnvironmentConflicts)
	}
	conflictReread(run, state, "read-2")
	cloudAgentRecordToolResult(run, state, conflictCall("write-3"), map[string]any{}, nil)
	if len(state.EnvironmentConflicts) != 0 {
		t.Fatalf("工具成功后冲突计数应清零：%+v", state.EnvironmentConflicts)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if _, ok := fields["environmentConflicts"]; ok {
		t.Fatal("清零后 environmentConflicts 应被 omitempty 省略")
	}
}
