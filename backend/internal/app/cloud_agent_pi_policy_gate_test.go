package app

import (
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// TestPiModelStepRejectsPromptWithoutServerPolicy 锁住"服务端策略不可被移除"这条合同。
//
// 背景：阶段 2 起，系统提示由 Node 装配（服务端策略前缀 + Harness 文件），
// Go 在 `PiModelStep` 只校验了**长度**。也就是说一个缺陷或篡改的 worker 只要少发一段
// system prompt，就能解除服务端对工具权限、能力边界与安全规则的约束。
//
// 这条校验与"压迫 Go 侧重新装配提示"无关：Go 只是要求**自己下发的那份策略仍然在**，
// Node 依然可以自由追加 Harness 内容。它也是 Node 侧 `renderSystemPrompt` 修复
//（服务端策略永远在最前、SYSTEM.md 不能替换它）的服务端兜底 —— 两侧都要有，
// 只修 Node 等于把强制层交给被校验方自己声明。
func TestPiModelStepRejectsPromptWithoutServerPolicy(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	_, runState := reloadPiRun(t, s, run.ID)
	if strings.TrimSpace(runState.Canonical.SystemPrompt) == "" {
		t.Fatal("夹具必须带服务端策略，否则本用例没有意义")
	}

	// 模拟"装配时把服务端策略整段丢掉，只留下工作区文件"。
	stripped := runState.Canonical
	stripped.SystemPrompt = "## Workspace AGENTS.md\n只有工作区文件，没有服务端策略"

	_, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: stripped})
	if err == nil {
		t.Fatal("缺少服务端策略的模型请求必须被拒绝")
	}
	if !strings.Contains(err.Error(), "服务端策略") {
		t.Fatalf("拒绝原因不是服务端策略缺失: %v", err)
	}
}

// TestPiModelStepAcceptsPolicyWithWorkspaceAppended：Node 的正常装配结果必须通过 ——
// 策略在前、Harness 内容追加在后。
func TestPiModelStepAcceptsPolicyWithWorkspaceAppended(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	_, runState := reloadPiRun(t, s, run.ID)

	assembled := runState.Canonical
	assembled.SystemPrompt = runState.Canonical.SystemPrompt +
		"\n\n## Workspace AGENTS.md\n工作区补充约定\n\n## Workspace SOUL.md\n人格设定"

	step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: assembled})
	if err != nil {
		t.Fatalf("正常装配结果被拒: %v", err)
	}
	if step.TaskID == "" {
		t.Fatal("正常装配应产生模型任务")
	}
}

// TestPiModelStepAcceptsPolicyBehindVersionHeader：判据用"包含"而不是"前缀"，
// 这样未来在策略前插入版本头之类的合法包装不会被误杀。
func TestPiModelStepAcceptsPolicyBehindVersionHeader(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	_, runState := reloadPiRun(t, s, run.ID)

	wrapped := runState.Canonical
	wrapped.SystemPrompt = "<!-- prompt-contract: v2 -->\n" + runState.Canonical.SystemPrompt

	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: wrapped}); err != nil {
		t.Fatalf("策略前带版本头时不应被拒: %v", err)
	}
}

// TestPiModelStepSkipsPolicyCheckWhenRunHasNoPolicy：旧运行可能没有策略前缀，
// 此时不得因为"空策略"而拒绝 —— 否则会把历史运行全部锁死。
func TestPiModelStepSkipsPolicyCheckWhenRunHasNoPolicy(t *testing.T) {
	s, _, run := piAgentTestLeasedFixture(t)
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Canonical.SystemPrompt = ""
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	current, currentState := reloadPiRun(t, s, run.ID)

	request := currentState.Canonical
	request.SystemPrompt = "任意提示"
	if _, err := s.PiModelStep("user", run.ID, current.LeaseOwner, PiModelStepRequest{Canonical: request}); err != nil {
		t.Fatalf("无策略前缀的运行不应被这条校验拒绝: %v", err)
	}
}
