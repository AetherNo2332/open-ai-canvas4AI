package agentservice

import (
	"encoding/json"
	"strings"
	"testing"
)

// fixtureRunSnapshot builds a fully valid snapshot for one definition role.
func fixtureRunSnapshot(id string) AgentRunSnapshot {
	return AgentRunSnapshot{
		ProtocolVersion:   PiExecutionProtocolVersion,
		OwnerID:           "user-1",
		SessionID:         "session-" + id,
		RunID:             "run-" + id,
		TriggerMessageID:  "msg-" + id,
		LeaseEpoch:        1,
		PiSessionRevision: 4,
		Definition:        Definition{ID: id + ".agent", Version: "1", Role: RoleBusiness, Application: "", SpaceTypes: nil, Title: "测试 Agent"},
		Scope:             Scope{Application: "canvas", SpaceType: SpaceCanvas, SpaceID: "canvas-1"},
		EffectiveTools: []EffectiveTool{{
			Name: "notebook_write", Description: "写入", Parameters: json.RawMessage(`{"type":"object"}`),
		}},
		ModelLimits: ModelLimits{ContextWindowTokens: 100000, MaxOutputTokens: 4000},
		Execution: ExecutionBinding{
			WorkItemID: "work-1", AssignmentID: "assign-1", AssignmentEpoch: 1,
			BudgetGrantID: "grant-1", AuthorizationVersion: 3,
		},
	}
}

func sampleSnapshots() map[string]AgentRunSnapshot {
	canvas := fixtureRunSnapshot("canvas")
	canvas.Definition = Definition{ID: "canvas.agent", Version: "1", Role: RoleBusiness, Application: ApplicationCanvas, SpaceTypes: []string{SpaceCanvas}, Title: "画布 Agent"}
	canvas.Scope = Scope{Application: ApplicationCanvas, SpaceType: SpaceCanvas, SpaceID: "canvas-1"}

	drama := fixtureRunSnapshot("drama")
	drama.Definition = Definition{ID: "drama.agent", Version: "1", Role: RoleBusiness, Application: ApplicationDrama, SpaceTypes: []string{SpaceDramaProject}, Title: "短剧 Agent"}
	drama.Scope = Scope{Application: ApplicationDrama, SpaceType: SpaceDramaProject, SpaceID: "project-1"}

	ecommerce := fixtureRunSnapshot("ecommerce")
	ecommerce.Definition = Definition{ID: "ecommerce.agent", Version: "1", Role: RoleBusiness, Application: ApplicationEcommerce, SpaceTypes: []string{SpaceWorkbenchBatch}, Title: "电商 Agent"}
	ecommerce.Scope = Scope{Application: ApplicationEcommerce, SpaceType: SpaceWorkbenchBatch, SpaceID: "batch-1"}

	supervisor := fixtureRunSnapshot("supervisor")
	supervisor.Definition = Definition{ID: DefSupervisorAgent, Version: "1", Role: RoleSupervisor, Application: ApplicationOrchestration, SpaceTypes: []string{SpaceWork}, Title: "总控 Agent"}
	supervisor.Scope = Scope{Application: ApplicationOrchestration, SpaceType: SpaceWork, SpaceID: "work-1"}

	return map[string]AgentRunSnapshot{"canvas": canvas, "drama": drama, "ecommerce": ecommerce, "supervisor": supervisor}
}

// C0-A: every agent role's snapshot carries the full field set and passes
// validation; anything incomplete fails with a deterministic reason.
func TestAgentRunSnapshotValidation(t *testing.T) {
	for name, snapshot := range sampleSnapshots() {
		if err := snapshot.Validate(); err != nil {
			t.Fatalf("%s snapshot should validate: %v", name, err)
		}
		if snapshot.ProtocolVersion != PiExecutionProtocolVersion {
			t.Fatalf("%s snapshot must carry the frozen protocol version", name)
		}
	}
	if _, ok := sampleSnapshots()["supervisor"]; !ok || DefSupervisorAgent != "supervisor.agent" {
		t.Fatalf("supervisor definition id must exist")
	}

	breaks := []struct {
		name   string
		reason string
		mutate func(*AgentRunSnapshot)
	}{
		{"missing owner", "执行者不能为空", func(s *AgentRunSnapshot) { s.OwnerID = " " }},
		{"missing session", "会话不能为空", func(s *AgentRunSnapshot) { s.SessionID = "" }},
		{"missing run", "运行不能为空", func(s *AgentRunSnapshot) { s.RunID = "" }},
		{"missing trigger message", "触发消息不能为空", func(s *AgentRunSnapshot) { s.TriggerMessageID = "" }},
		{"non-positive lease epoch", "租约代次无效", func(s *AgentRunSnapshot) { s.LeaseEpoch = 0 }},
		{"scope lacking space id", "会话范围必须包含业务、空间类型与空间 ID", func(s *AgentRunSnapshot) { s.Scope.SpaceID = "" }},
		{"definition role invalid", "Agent 定义角色无效", func(s *AgentRunSnapshot) { s.Definition.Role = "misc" }},
		{"work binding missing grant", "工作执行缺少预算授权", func(s *AgentRunSnapshot) { s.Execution.BudgetGrantID = "" }},
		{"work binding missing authorization version", "工作执行缺少授权版本", func(s *AgentRunSnapshot) { s.Execution.AuthorizationVersion = 0 }},
		{"assignment missing epoch", "工作执行缺少 Assignment 代次", func(s *AgentRunSnapshot) { s.Execution.AssignmentEpoch = 0 }},
		{"tool missing schema", "工具缺少参数 Schema", func(s *AgentRunSnapshot) { s.EffectiveTools[0].Parameters = nil }},
		{"model limit missing window", "模型限制缺少上下文窗口", func(s *AgentRunSnapshot) { s.ModelLimits.ContextWindowTokens = 0 }},
		{"foreign protocol", "执行协议版本不一致", func(s *AgentRunSnapshot) { s.ProtocolVersion = "pi-execution/v9" }},
	}
	for _, tt := range breaks {
		snapshot := sampleSnapshots()["drama"]
		tt.mutate(&snapshot)
		err := snapshot.Validate()
		if err == nil {
			t.Fatalf("%s: expected failure", tt.name)
		}
		if !strings.Contains(err.Error(), tt.reason) {
			t.Fatalf("%s: reason mismatch: %v", tt.name, err)
		}
	}
}

// C0-A: an execution outside a work tree drops the binding instead of faking it.
func TestAgentRunSnapshotStandaloneExecution(t *testing.T) {
	snapshot := fixtureRunSnapshot("independent")
	snapshot.Execution = ExecutionBinding{}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("standalone snapshot should validate: %v", err)
	}
	if snapshot.Execution.WorkItemID != "" {
		t.Fatalf("standalone snapshot must not carry a work item")
	}
}

// C0-A: the session message contract sorts owner, supervisor and system input
// into one ordered queue with a stable idempotent MessageID.
func TestSessionMessageContract(t *testing.T) {
	valid := SessionMessage{
		TargetSessionID: "session-1", SenderUserID: "user-1", Actor: ActorUser,
		MessageID: "msg-1", Body: "调整第二集分镜",
		RunID: "run-s1", LeaseEpoch: 2, WorkItemID: "work-1",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid: %v", err)
	}

	invalid := []struct {
		name   string
		reason string
		mutate func(*SessionMessage)
	}{
		{"missing target", "目标会话不能为空", func(m *SessionMessage) { m.TargetSessionID = "" }},
		{"missing sender", "发送者不能为空", func(m *SessionMessage) { m.SenderUserID = "" }},
		{"missing message id", "消息 ID 不能为空", func(m *SessionMessage) { m.MessageID = "" }},
		{"actor not allowed", "消息来源无效", func(m *SessionMessage) { m.Actor = "model" }},
		{"empty body without resources", "消息正文与资源引用不能同时为空", func(m *SessionMessage) { m.Body = "" }},
		{"missing run identity", "发送执行缺少运行身份", func(m *SessionMessage) { m.RunID = "" }},
		{"non-positive lease epoch", "消息执行缺少租约代次", func(m *SessionMessage) { m.LeaseEpoch = 0 }},
		{"resource refs malformed", "资源引用不能为空", func(m *SessionMessage) { m.Body = ""; m.ResourceRefs = []string{" "} }},
	}
	for _, tt := range invalid {
		message := valid
		tt.mutate(&message)
		err := message.Validate()
		if err == nil {
			t.Fatalf("%s: expected failure", tt.name)
		}
		if !strings.Contains(err.Error(), tt.reason) {
			t.Fatalf("%s: reason mismatch: %v", tt.name, err)
		}
	}

	withRefs := valid
	withRefs.Body = ""
	withRefs.ResourceRefs = []string{"asset:version:9"}
	if err := withRefs.Validate(); err != nil {
		t.Fatalf("resource-only message should be valid: %v", err)
	}
}

// C0-A: recovery reports carry the failure class vocabulary so worker retries
// and audit share one encoding.
func TestRecoveryReportContract(t *testing.T) {
	valid := RecoveryReport{
		SessionID: "session-1", RunID: "run-1", LeaseEpoch: 3,
		FailureClass: FailureClassNetwork, Operation: RecoveryOperationModelStep,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid: %v", err)
	}
	if RecoveryFailureClasses[FailureClassNetwork] == "" || RecoveryOperations[RecoveryOperationModelStep] == "" {
		t.Fatalf("failure class and operation vocabularies must be declared")
	}

	invalid := []struct {
		name   string
		reason string
		mutate func(*RecoveryReport)
	}{
		{"unknown failure class", "恢复类别无效", func(r *RecoveryReport) { r.FailureClass = "meteor" }},
		{"unknown operation", "恢复操作无效", func(r *RecoveryReport) { r.Operation = "coffee" }},
		{"missing session", "会话不能为空", func(r *RecoveryReport) { r.SessionID = "" }},
		{"missing run", "运行不能为空", func(r *RecoveryReport) { r.RunID = "" }},
		{"missing lease epoch", "恢复缺少租约代次", func(r *RecoveryReport) { r.LeaseEpoch = 0 }},
	}
	for _, tt := range invalid {
		report := valid
		tt.mutate(&report)
		err := report.Validate()
		if err == nil {
			t.Fatalf("%s: expected failure", tt.name)
		}
		if !strings.Contains(err.Error(), tt.reason) {
			t.Fatalf("%s: reason mismatch: %v", tt.name, err)
		}
	}
}
