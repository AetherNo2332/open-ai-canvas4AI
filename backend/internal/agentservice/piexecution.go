package agentservice

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Pi execution contract shared by the Go control plane and the Pi worker.
//
// The canvas worker already runs one execution shape (run claim, model steps,
// tool batches, checkpoints). This contract is the business-neutral form of
// that shape: it is written once here, frozen per run by the Go host, and the
// worker treats every field as authoritative server state.

const (
	// PiExecutionProtocolVersion is the frozen execution protocol selected for
	// every dispatched message. A run always carries one version; a worker
	// that cannot speak it must not claim or resume the run.
	PiExecutionProtocolVersion = "pi-execution/v1"

	// DefSupervisorAgent is the supervisor definition registered by the host.
	DefSupervisorAgent = "supervisor.agent"

	// Remove the duplicated actor constants; keep only execution vocabulary.
)

var actorRoles = map[string]string{
	ActorUser:       "user",
	ActorSupervisor: "user",
	ActorSystem:     "system",
	ActorExecutor:   "system",
}

// ModelLimits bounds one run's context admission; the values come from the
// Go model configuration, never from the worker.
type ModelLimits struct {
	ContextWindowTokens  int    `json:"contextWindowTokens"`
	MaxOutputTokens      int    `json:"maxOutputTokens"`
	ReservedOutputTokens int    `json:"reservedOutputTokens,omitempty"`
	CompactAtTokens      int    `json:"compactAtTokens,omitempty"`
	KeepRecentTokens     int    `json:"keepRecentTokens,omitempty"`
	Configured           bool   `json:"configured"`
	Source               string `json:"source"`
	Version              string `json:"version,omitempty"`
	Digest               string `json:"digest,omitempty"`
}

// EffectiveTool is one tool admitted into this run. Exposure is not
// authorization: the Go gateway re-checks every invocation.
type EffectiveTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Charges     bool            `json:"charges,omitempty"`
	// ApprovalRequired marks tools whose execution must first persist an
	// approval-bound operation record.
	ApprovalRequired bool `json:"approvalRequired,omitempty"`
}

// ExecutionBinding is the trusted work binding resolved by Go from the
// authorization records. A run outside a work tree leaves it empty rather
// than fabricating a root.
type ExecutionBinding struct {
	WorkItemID           string `json:"workItemId,omitempty"`
	AssignmentID         string `json:"assignmentId,omitempty"`
	AssignmentEpoch      int64  `json:"assignmentEpoch,omitempty"`
	BudgetGrantID        string `json:"budgetGrantId,omitempty"`
	AuthorizationVersion int64  `json:"authorizationVersion,omitempty"`
}

// AgentRunSnapshot is everything a worker needs to drive one Pi run: who owns
// it, which Session and run it serves, the frozen definition/scope/tools, and
// the execution binding carrying billing and fencing identity.
type AgentRunSnapshot struct {
	ProtocolVersion   string           `json:"protocolVersion"`
	OwnerID           string           `json:"ownerId"`
	SessionID         string           `json:"sessionId"`
	RunID             string           `json:"runId"`
	TriggerMessageID  string           `json:"triggerMessageId"`
	LeaseEpoch        int64            `json:"leaseEpoch"`
	PiSessionRevision int64            `json:"piSessionRevision,omitempty"`
	Definition        Definition       `json:"definition"`
	Scope             Scope            `json:"scope"`
	EffectiveTools    []EffectiveTool  `json:"effectiveTools"`
	ModelLimits       ModelLimits      `json:"modelLimits"`
	Execution         ExecutionBinding `json:"execution,omitempty"`
	// Prompt is the frozen system prompt for this run; Skills snapshot the
	// skill material so a resumed run keeps the same contract.
	Prompt string              `json:"prompt,omitempty"`
	Skills []SkillSnapshotView `json:"skills,omitempty"`
}

// SkillSnapshotView carries the skill identity a run freezes.
type SkillSnapshotView struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Hash    string `json:"hash,omitempty"`
}

func (s AgentRunSnapshot) workBound() bool { return strings.TrimSpace(s.Execution.WorkItemID) != "" }

// Validate enforces the field and failure semantics each of the four agent
// kinds must satisfy before a worker may act on the snapshot.
func (s AgentRunSnapshot) Validate() error {
	if err := s.validateIdentity(); err != nil {
		return err
	}
	if err := s.validateWorkBinding(); err != nil {
		return err
	}
	return s.validateTools()
}

func (s AgentRunSnapshot) validateIdentity() error {
	if s.ProtocolVersion != PiExecutionProtocolVersion {
		return NewError(CodeInvalidRequest, "执行协议版本不一致")
	}
	if strings.TrimSpace(s.OwnerID) == "" {
		return NewError(CodeUnauthorized, "执行者不能为空")
	}
	if strings.TrimSpace(s.SessionID) == "" {
		return NewError(CodeInvalidRequest, "会话不能为空")
	}
	if strings.TrimSpace(s.RunID) == "" {
		return NewError(CodeInvalidRequest, "运行不能为空")
	}
	if strings.TrimSpace(s.TriggerMessageID) == "" {
		return NewError(CodeInvalidRequest, "触发消息不能为空")
	}
	if s.LeaseEpoch <= 0 {
		return NewError(CodeStaleLease, "租约代次无效")
	}
	if s.Definition.Role != RoleBusiness && s.Definition.Role != RoleSupervisor {
		return NewError(CodeInvalidRequest, "Agent 定义角色无效")
	}
	if strings.TrimSpace(s.Definition.ID) == "" {
		return NewError(CodeInvalidRequest, "Agent 定义不能为空")
	}
	scope := s.Scope.Normalized()
	if scope.Application == "" || scope.SpaceType == "" || scope.SpaceID == "" {
		return NewError(CodeScopeMismatch, "会话范围必须包含业务、空间类型与空间 ID")
	}
	return nil
}

func (s AgentRunSnapshot) validateWorkBinding() error {
	if !s.workBound() {
		return nil
	}
	if strings.TrimSpace(s.Execution.BudgetGrantID) == "" {
		return NewError(CodeBudgetExceeded, "工作执行缺少预算授权")
	}
	if s.Execution.AuthorizationVersion <= 0 {
		return NewError(CodeInvalidRequest, "工作执行缺少授权版本")
	}
	if s.Execution.AssignmentEpoch <= 0 {
		return NewError(CodeStaleAssignment, "工作执行缺少 Assignment 代次")
	}
	if strings.TrimSpace(s.Execution.AssignmentID) == "" {
		return NewError(CodeStaleAssignment, "工作执行缺少 Assignment")
	}
	return nil
}

func (s AgentRunSnapshot) validateTools() error {
	if len(s.EffectiveTools) == 0 {
		if s.Definition.Role == RoleBusiness {
			return NewError(CodeInvalidRequest, "业务执行缺少有效工具")
		}
	} else {
		for _, tool := range s.EffectiveTools {
			if strings.TrimSpace(tool.Name) == "" || len(tool.Parameters) == 0 {
				return NewError(CodeInvalidRequest, "工具缺少参数 Schema")
			}
		}
	}
	if s.ModelLimits.ContextWindowTokens <= 0 {
		return NewError(CodeInvalidRequest, "模型限制缺少上下文窗口")
	}
	return nil
}

// SessionMessage is one message entering the ordered per-Session input queue.
// Owner input and supervisor dispatch meet here; the MessageID is the
// idempotency key and survives retries, redelivery and worker restarts.
type SessionMessage struct {
	TargetSessionID string   `json:"targetSessionId"`
	SenderUserID    string   `json:"senderUserId"`
	Actor           string   `json:"actor"`
	MessageID       string   `json:"messageId"`
	Body            string   `json:"body"`
	ResourceRefs    []string `json:"resourceRefs,omitempty"`
	// RunID + LeaseEpoch carry the sending execution's identity so the
	// receiving session can audit who delivered the message.
	RunID      string `json:"runId,omitempty"`
	LeaseEpoch int64  `json:"leaseEpoch,omitempty"`
	// WorkItemID binds delivery-time work governance for the receiving side.
	WorkItemID string `json:"workItemId,omitempty"`
}

// Validate enforces the shared queue admission semantics.
func (m SessionMessage) Validate() error {
	if strings.TrimSpace(m.TargetSessionID) == "" {
		return NewError(CodeInvalidRequest, "目标会话不能为空")
	}
	if strings.TrimSpace(m.SenderUserID) == "" {
		return NewError(CodeUnauthorized, "发送者不能为空")
	}
	if _, ok := actorRoles[m.Actor]; !ok {
		return NewError(CodeForbidden, "消息来源无效")
	}
	if strings.TrimSpace(m.MessageID) == "" {
		return NewError(CodeIdempotencyConflict, "消息 ID 不能为空")
	}
	body := strings.TrimSpace(m.Body)
	if body == "" && len(m.ResourceRefs) == 0 {
		return NewError(CodeInvalidRequest, "消息正文与资源引用不能同时为空")
	}
	for _, ref := range m.ResourceRefs {
		if strings.TrimSpace(ref) == "" {
			return NewError(CodeInvalidRequest, "资源引用不能为空")
		}
	}
	if strings.TrimSpace(m.RunID) == "" {
		return NewError(CodeInvalidRequest, "发送执行缺少运行身份")
	}
	if m.LeaseEpoch <= 0 {
		return NewError(CodeInvalidRequest, "消息执行缺少租约代次")
	}
	return nil
}

// Recovery failure vocabulary: the worker classifies an ambiguous bridge
// failure and the Go host decides retry, resume or audit.
const (
	FailureClassConflict    = "conflict"
	FailureClassRateLimited = "rate_limited"
	FailureClassTimeout     = "timeout"
	FailureClassHTTP5xx     = "http_5xx"
	FailureClassNetwork     = "network"
)

var RecoveryFailureClasses = map[string]string{
	FailureClassConflict:    "并发冲突",
	FailureClassRateLimited: "限流",
	FailureClassTimeout:     "超时",
	FailureClassHTTP5xx:     "服务错误",
	FailureClassNetwork:     "网络错误",
}

// Recovery operations name the bridge call surface a failure occurred on.
const (
	RecoveryOperationModelStep  = "model"
	RecoveryOperationToolCall   = "tool"
	RecoveryOperationCheckpoint = "checkpoint"
	RecoveryOperationCompaction = "compaction"
	RecoveryOperationControl    = "control"
	RecoveryOperationPhase      = "phase"
	RecoveryOperationRenew      = "renew"
	RecoveryOperationSnapshot   = "snapshot"
)

var RecoveryOperations = map[string]string{
	RecoveryOperationModelStep:  "模型步骤",
	RecoveryOperationToolCall:   "工具调用",
	RecoveryOperationCheckpoint: "检查点",
	RecoveryOperationCompaction: "上下文压缩",
	RecoveryOperationControl:    "控制查询",
	RecoveryOperationPhase:      "阶段上报",
	RecoveryOperationRenew:      "租约续期",
	RecoveryOperationSnapshot:   "快照",
}

// RecoveryReport is the worker's durable recovery request after an ambiguous
// operation. The host re-reads authoritative state instead of replaying the
// failed request.
type RecoveryReport struct {
	SessionID    string `json:"sessionId"`
	RunID        string `json:"runId"`
	LeaseEpoch   int64  `json:"leaseEpoch"`
	FailureClass string `json:"failureClass"`
	Operation    string `json:"operation"`
	TaskID       string `json:"taskId,omitempty"`
	CallID       string `json:"callId,omitempty"`
	HTTPStatus   int    `json:"httpStatus,omitempty"`
	RetryAfterMS int64  `json:"retryAfterMs,omitempty"`
}

// Validate enforces the report vocabulary; unknown classes are refused
// instead of silently widening the audit contract.
func (r RecoveryReport) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return NewError(CodeInvalidRequest, "会话不能为空")
	}
	if strings.TrimSpace(r.RunID) == "" {
		return NewError(CodeInvalidRequest, "运行不能为空")
	}
	if r.LeaseEpoch <= 0 {
		return NewError(CodeInvalidRequest, "恢复缺少租约代次")
	}
	if _, ok := RecoveryFailureClasses[r.FailureClass]; !ok {
		return fmt.Errorf("恢复类别无效: %s", r.FailureClass)
	}
	if _, ok := RecoveryOperations[r.Operation]; !ok {
		return fmt.Errorf("恢复操作无效: %s", r.Operation)
	}
	return nil
}
