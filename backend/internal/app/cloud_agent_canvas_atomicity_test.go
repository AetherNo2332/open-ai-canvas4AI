package app

import (
	"encoding/json"
	"errors"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 阶段 2.1 故障注入：画布保存与"变更记录"共用同一个 MutateCloudAgent 事务。
// recorder 失败时**必须整笔回滚** —— 否则会出现"画布已改、回执却报失败"的不一致：
// 模型看到失败回执会重试写入，而画布其实已经被改过一次。
//
// 这个用例同时证明"为什么必须有分类"：把基础设施错误当业务回执吞掉（返回 nil）时，
// 画布会带着部分写入提交；把它向上返回时，整个事务回滚。
func TestCanvasWriteRollsBackWhenRecorderFails(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	const canvasID = "agent-canvas"
	before := canvasPayloadJSON(t, s)
	recorderErr := errors.New("injected recorder failure")
	failRecorder := func(_ *repository.Repository, _ cloudAgentMutationInput) error { return recorderErr }

	// ① 修复后的行为：基础设施错误向上返回 → 事务回滚 → 画布不变。
	call := addNodeCall(t, "call-fail", args.SnapshotHash, "atomic-fail", "注入失败")
	err = s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(_ *model.CloudAgentExecution, repo *repository.Repository) error {
		_, writeErr := applyCloudAgentCanvas(repo, "user", canvasID, call, policy, failRecorder)
		if writeErr != nil && !cloudAgentToolErrorIsBusiness(writeErr) {
			return writeErr // 与 advanceCloudAgentTool 的新分类一致
		}
		return nil
	})
	if !errors.Is(err, recorderErr) {
		t.Fatalf("recorder 错误应向上返回以触发回滚，实际: %v", err)
	}
	if after := canvasPayloadJSON(t, s); after != before {
		t.Fatalf("回滚后画布不得保留部分写入\nbefore=%s\nafter=%s", before, after)
	}
	if containsJSONText(canvasPayloadJSON(t, s), "注入失败") {
		t.Fatal("注入的节点不应出现在画布上")
	}

	// ② 反证：若把同一错误当业务回执吞掉（返回 nil），画布会带着写入提交。
	// 这正是修复前 advanceCloudAgentTool 的行为，用来说明分类不可省。
	current, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	swallowed := addNodeCall(t, "call-swallow", args.SnapshotHash, "atomic-swallow", "被吞掉的写入")
	if err := s.repo.MutateCloudAgent("user", run.ID, current.Revision, func(_ *model.CloudAgentExecution, repo *repository.Repository) error {
		_, _ = applyCloudAgentCanvas(repo, "user", canvasID, swallowed, policy, failRecorder)
		return nil // 吞掉基础设施错误
	}); err != nil {
		t.Fatal(err)
	}
	if !containsJSONText(canvasPayloadJSON(t, s), "被吞掉的写入") {
		t.Fatal("反证失败：吞掉错误时画布本应保留写入（说明这类错误必须被分类拦住）")
	}
	_ = state
}

// addNodeCall 构造一个会真正新增节点的写入调用：update_node 要求节点已存在，
// 用它测"写入是否落盘"会先被参数校验挡掉，测不到事务边界。
func addNodeCall(t *testing.T, id, snapshotHash, nodeID, title string) cloudAgentCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"snapshotHash": snapshotHash,
		"ops": []map[string]any{
			{"type": "add_node", "id": nodeID, "nodeType": "text", "title": title, "content": title},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: id}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = string(raw)
	return call
}

func containsJSONText(payload, needle string) bool {
	return len(payload) > 0 && len(needle) > 0 && (func() bool {
		for i := 0; i+len(needle) <= len(payload); i++ {
			if payload[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
