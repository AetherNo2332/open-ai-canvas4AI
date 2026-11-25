package app

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
)

// agentOldDirectoryMutations 覆盖旧执行合同的可执行形态：业务 read 扩展时期的目录 1、
// 更早的 legacy 0，以及完全没有冻结目录的旧记录。
var agentOldDirectoryMutations = []struct {
	name   string
	mutate func(*cloudAgentRuntime)
}{
	{"old directory 1", func(state *cloudAgentRuntime) { state.Capabilities.DirectoryVersion = 1 }},
	{"legacy directory 0", func(state *cloudAgentRuntime) { state.Capabilities.DirectoryVersion = 0 }},
	{"missing directory", func(state *cloudAgentRuntime) { state.Capabilities = nil }},
}

func saveRunState(t *testing.T, service *Service, db *gorm.DB, runID string, mutate func(*cloudAgentRuntime)) {
	t.Helper()
	run, state := reloadPiRun(t, service, runID)
	mutate(state)
	if err := cloudAgentSave(run, state); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(run).Error; err != nil {
		t.Fatal(err)
	}
}

func requireDirectoryUnavailable(t *testing.T, err error) {
	t.Helper()
	var denied *kernel.AppError
	if !errors.As(err, &denied) || denied.Reason != "agent_capability_directory_unavailable" {
		t.Fatalf("old read contract was not rejected as an incompatible directory: %v", err)
	}
}

// TestAgentReadContractUpgradeRejectsOldExecution 守住发布边界：目录 1、legacy 0 和
// 无目录的可执行恢复都不能绕过新的 Skill-only read 合同。
func TestAgentReadContractUpgradeRejectsOldExecution(t *testing.T) {
	for _, item := range agentOldDirectoryMutations {
		t.Run(item.name+"/queued claim", func(t *testing.T) {
			service, db := agentRunFixture(t)
			created, err := service.CreateCloudAgentRun("user", agentTestRequest(), "")
			if err != nil {
				t.Fatal(err)
			}
			saveRunState(t, service, db, created.ID, item.mutate)
			claimed, err := service.ClaimPiAgent("worker")
			if claimed != nil {
				t.Fatalf("old directory was handed to a worker: %+v", claimed)
			}
			requireDirectoryUnavailable(t, err)
			terminal, _ := reloadPiRun(t, service, created.ID)
			if terminal.Status != "failed" || len(openOrdersFor(t, db, created.ID)) != 0 {
				t.Fatalf("rejected upgrade did not settle: status=%s", terminal.Status)
			}
			assertReservationInvariant(t, db, "user")
		})
		t.Run(item.name+"/returning worker", func(t *testing.T) {
			service, db := agentRunFixture(t)
			created, err := service.CreateCloudAgentRun("user", agentTestRequest(), "")
			if err != nil {
				t.Fatal(err)
			}
			run, _ := startPiAgentFirstStep(t, service, created.ID)
			saveRunState(t, service, db, created.ID, item.mutate)
			if _, err := service.PiAgentSnapshot("user", created.ID, run.LeaseOwner); err == nil {
				t.Fatal("old directory served a fresh worker snapshot")
			} else {
				requireDirectoryUnavailable(t, err)
			}
			// 旧运行已终结：工具批次既不执行也不落库。终结前提交的批次同样在准入阶段被拒。
			batch := PiToolBatchRequest{TaskID: "any-task", Calls: []cloudAgentCall{
				piAgentTestCall("read-business", "read", `{"path":"canvas:text/shot-1"}`),
			}}
			_ = service.PiToolBatch("user", created.ID, run.LeaseOwner, batch)
			terminal, terminalState := reloadPiRun(t, service, created.ID)
			if len(terminalState.Calls) != 0 {
				t.Fatalf("old directory installed a durable tool batch: %+v", terminalState.Calls)
			}
			if terminal.Status != "failed" || len(openOrdersFor(t, db, created.ID)) != 0 {
				t.Fatalf("returning worker rejection did not settle: status=%s", terminal.Status)
			}
			assertReservationInvariant(t, db, "user")
		})
	}
}

// TestAgentReadContractUpgradePreservesHistory 证明升级只影响可执行路径：旧会话仍可
// 查阅、按权限取消与归档，并能按新定义和新目录创建新一轮。
func TestAgentReadContractUpgradePreservesHistory(t *testing.T) {
	service, db := agentRunFixture(t)
	created, err := service.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	saveRunState(t, service, db, created.ID, func(state *cloudAgentRuntime) { state.Capabilities.DirectoryVersion = 1 })
	if err := service.CancelCloudAgent(context.Background(), "user", created.ID); err != nil {
		t.Fatalf("user could not cancel the old run: %v", err)
	}
	if _, err := service.CloudAgentRun("other", created.ID); err == nil {
		t.Fatal("another user read the historical run")
	}
	historical, err := service.CloudAgentRun("user", created.ID)
	if err != nil || historical.ID != created.ID || historical.Status != "cancelled" {
		t.Fatalf("history was lost: %+v %v", historical, err)
	}
	if err := service.ArchiveCloudAgentConversation("user", created.ConversationID); err != nil {
		t.Fatalf("user could not archive the old conversation: %v", err)
	}
	request := agentTestRequest()
	request.IdempotencyKey = "after-read-contract-upgrade"
	fresh, err := service.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatalf("a new round could not start after the upgrade: %v", err)
	}
	_, state := reloadPiRun(t, service, fresh.ID)
	if state.Capabilities.DirectoryVersion != agentCapabilityDirectoryVersion ||
		state.Capabilities.DefinitionID != "canvas.default" || state.Capabilities.DefinitionVersion != 2 {
		t.Fatalf("new round did not freeze the new contract: %+v", state.Capabilities)
	}
	if _, present := state.Capabilities.Tools["read"]; present {
		t.Fatal("canvas round without Skills still disclosed read")
	}
	if !containsString(cloudAgentToolNames(state.Canonical.Tools), "canvas_read_content") {
		t.Fatalf("new round lost the named canvas read tool: %v", cloudAgentToolNames(state.Canonical.Tools))
	}
	// 历史调用不重新执行：旧运行保持终态且没有新增工具回执。
	after, _ := reloadPiRun(t, service, created.ID)
	if after.Status != "cancelled" || len(openOrdersFor(t, db, created.ID)) != 0 {
		t.Fatalf("history changed during the upgrade: status=%s", after.Status)
	}
	if len(state.Calls) != 0 {
		t.Fatalf("new round inherited historical tool calls: %+v", state.Calls)
	}
}
