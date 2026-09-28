package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// clearPiActiveTask 模拟"上一步已结束、可以发起下一步"。
//
// PiModelStep 在 ActiveTaskID 非空时会直接返回既有任务视图（幂等短路），
// 所以要让"第二步"真正走到提示合同校验，必须先清掉活动任务。
func clearPiActiveTask(t *testing.T, s *Service, runID string) *model.CloudAgentExecution {
	t.Helper()
	run, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.ActiveTaskID = ""
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	refreshed, _ := reloadPiRun(t, s, runID)
	return refreshed
}

// TestPiModelStepFreezesPromptContractOnFirstStep：首个模型步把 Harness 身份固化进运行状态。
//
// 这是"首步与后续步使用同一份不可变快照"的服务端落点：Node 在 worker 启动时读一次 Harness，
// 改了文件再重启会让在途运行静默换系统提示，所以身份必须在第一步就钉死。
func TestPiModelStepFreezesPromptContractOnFirstStep(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	_, state := reloadPiRun(t, s, run.ID)
	if state.PromptContract != "" {
		t.Fatal("前提：新运行不应带有提示合同")
	}

	const hash = "a1b2c3d4e5f6"
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical, HarnessHash: hash}); err != nil {
		t.Fatalf("首个模型步失败: %v", err)
	}
	_, after := reloadPiRun(t, s, run.ID)
	if after.PromptContract != hash {
		t.Fatalf("提示合同 = %q，期望 %q", after.PromptContract, hash)
	}
}

// TestPiModelStepAcceptsSamePromptContractOnLaterSteps：同一步骤重投、以及后续步骤
// 使用同一份 Harness 时必须通过。
func TestPiModelStepAcceptsSamePromptContractOnLaterSteps(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	_, state := reloadPiRun(t, s, run.ID)

	const hash = "same-hash-0001"
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical, HarnessHash: hash}); err != nil {
		t.Fatal(err)
	}
	current := clearPiActiveTask(t, s, run.ID)
	_, next := reloadPiRun(t, s, run.ID)
	if next.PromptContract != hash {
		t.Fatalf("清理活动任务不应影响已固化的合同: %q", next.PromptContract)
	}
	if _, err := s.PiModelStep("user", run.ID, current.LeaseOwner, PiModelStepRequest{Canonical: next.Canonical, HarnessHash: hash}); err != nil {
		t.Fatalf("后续步骤沿用同一 Harness 必须通过: %v", err)
	}
}

// TestPiModelStepRejectsPromptContractDrift 是本轮的核心断言：
// worker 换了 Harness（改了磁盘文件并重启）之后，在途运行必须**明确停止**，
// 而不是静默换掉系统提示继续跑。
func TestPiModelStepRejectsPromptContractDrift(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	_, state := reloadPiRun(t, s, run.ID)

	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical, HarnessHash: "hash-before"}); err != nil {
		t.Fatal(err)
	}
	current := clearPiActiveTask(t, s, run.ID)
	_, next := reloadPiRun(t, s, run.ID)

	_, err := s.PiModelStep("user", run.ID, current.LeaseOwner, PiModelStepRequest{Canonical: next.Canonical, HarnessHash: "hash-after"})
	if err == nil {
		t.Fatal("Harness 变更后的步骤必须被拒绝，否则同一轮运行的系统提示会中途改变")
	}
	if !strings.Contains(err.Error(), "提示合同") {
		t.Fatalf("拒绝原因不是提示合同漂移: %v", err)
	}
	// 拒绝不得污染已固化的合同。
	_, latest := reloadPiRun(t, s, run.ID)
	if latest.PromptContract != "hash-before" {
		t.Fatalf("被拒的请求不应改写提示合同: %q", latest.PromptContract)
	}
}

// TestPiModelStepWithoutHarnessHashKeepsLegacyBehavior：没有 Harness 的部署
//（未配置 CANVAS_AGENT_HARNESS_DIR）不发这个字段，行为必须与迁移前一致。
func TestPiModelStepWithoutHarnessHashKeepsLegacyBehavior(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	_, state := reloadPiRun(t, s, run.ID)

	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: state.Canonical}); err != nil {
		t.Fatalf("不带 Harness 身份时不应受影响: %v", err)
	}
	_, after := reloadPiRun(t, s, run.ID)
	if after.PromptContract != "" {
		t.Fatalf("没有身份时不应凭空写入合同: %q", after.PromptContract)
	}
}

// TestPiModelStepWireAcceptsHarnessHash：路由用 DisallowUnknownFields 解码，
// 未声明的字段会让整个请求变成空 400 —— 阶段 1 就踩过这个坑（`type` 字段）。
// 这里用与路由相同的解码方式验证 producer 形态能被接受。
func TestPiModelStepWireAcceptsHarnessHash(t *testing.T) {
	body := `{"canonical":{"systemPrompt":"策略","messages":[{"role":"user","content":"hi"}]},"harnessHash":"deadbeef"}`
	var request PiModelStepRequest
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("producer 形态未被接受（会变成空 400）: %v", err)
	}
	if request.HarnessHash != "deadbeef" {
		t.Fatalf("harnessHash 未解码: %q", request.HarnessHash)
	}
}
