// Package document 是用户长期记忆文件的文档行为归属：条目编辑规则、修订历史、
// 待确认审批、撤销与 Agent 写入。app 保留 run 快照冻结/注入、工具事件适配与
// 管理员跨用户聚合；本包不接受 canonicalAgentRequest、CloudAgent runtime 或完整 Service。
//
// 依赖方向：document → repository/model/agentmemory/kernel。agentmemory 根包保持纯规则，
// 不反向依赖本包或 database。
package document

import (
	"fmt"
	"time"

	"yingce/backend/internal/agentmemory"
	"yingce/backend/internal/model"
)

const (
	// DefaultCapacity 是未显式配置时的记忆容量（字）。
	DefaultCapacity = agentmemory.DefaultCapacity
	// EntryMaxRunes 是单条记忆的字符上限。
	EntryMaxRunes = agentmemory.EntryMaxRunes
	// EntriesMax 是一份记忆文件的条目数上限。
	EntriesMax = agentmemory.EntriesMax
	// OpsMax 是一次 memory_update 允许的操作数。
	OpsMax = 10
	// CallsPerRun 限制单轮运行内的记忆写入次数。
	CallsPerRun = 8
	// RevisionKeep 是保留的修订数；待确认修订不参与裁剪。
	RevisionKeep = 100
	// PendingMax 是待确认修订的堆积上限。
	PendingMax = 20
	// ReasonMaxRunes 限制修改原因的长度。
	ReasonMaxRunes = 120
	// WriteAttempts 是并发冲突时的 CAS 重试次数。
	WriteAttempts = 3
)

// Outcome 状态与现有工具返回、运行事件保持一致。
const (
	OutcomeApplied   = "applied"
	OutcomePending   = "pending_review"
	OutcomeUnchanged = "unchanged"
)

// Change 是一条记忆的变化，用于修订记录、撤销和界面展示。
type Change struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Section string `json:"section"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
}

// Operation 是 Agent 一次记忆修改中的单个操作。
type Operation struct {
	Action  string `json:"action"`
	ID      string `json:"id,omitempty"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text,omitempty"`
}

// CapacityError 表示整批修改后超出记忆容量；只按整批结果检查，模型可在同一次调用里先合并再新增。
type CapacityError struct {
	Used, After, Capacity int
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("记忆已用 %d/%d 字，这批修改后为 %d 字，超出 %d 字。请在同一次调用里先 replace 合并或 remove 过时条目，再 add 新内容（只按整批结果检查容量）", e.Used, e.Capacity, e.After, e.After-e.Capacity)
}

// CapacityDetail 在容量超限时把当前全部条目交还给 Agent，它才能在下一次调用里合并。
type CapacityDetail struct {
	Usage    string
	OverBy   int
	Entries  []EntrySummary
	Guidance string
}

// EntrySummary 是容量明细里的一条既有条目。
type EntrySummary struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Text    string `json:"text"`
}

// Edit 是一批操作的应用结果。
type Edit struct {
	Entries    []model.AgentMemoryEntry
	NextSeq    int64
	Changes    []Change
	Duplicates int
}

// WriteOutcome 是一次记忆写入的结果，同时用于工具返回、运行事件和幂等重放。
type WriteOutcome struct {
	Status     string
	RevisionID string
	Summary    string
	Changes    []Change
	Duplicates int
	CharCount  int
	Capacity   int
}

// View 是用户看到的记忆文件。
type View struct {
	Mode         string                   `json:"mode"`
	Capacity     int                      `json:"capacity"`
	CharCount    int                      `json:"charCount"`
	Revision     int64                    `json:"revision"`
	Entries      []model.AgentMemoryEntry `json:"entries"`
	PendingCount int64                    `json:"pendingCount"`
	UpdatedAt    time.Time                `json:"updatedAt"`
}

// AdminView 是管理员看到的记忆文件正文。
type AdminView struct {
	UserID      string `json:"userId"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	View
}

// RevisionView 是一次修改的记录；changes 由前后快照按条目 ID 计算。
type RevisionView struct {
	ID           string     `json:"id"`
	Actor        string     `json:"actor"`
	RunID        string     `json:"runId,omitempty"`
	Status       string     `json:"status"`
	Summary      string     `json:"summary"`
	BaseRevision int64      `json:"baseRevision"`
	Revision     int64      `json:"revision"`
	Changes      []Change   `json:"changes"`
	CanUndo      bool       `json:"canUndo"`
	CreatedAt    time.Time  `json:"createdAt"`
	DecidedAt    *time.Time `json:"decidedAt,omitempty"`
	RestoredAt   *time.Time `json:"restoredAt,omitempty"`
}

// EntryInput 是用户手动编辑提交的单条条目。
type EntryInput struct {
	ID      string `json:"id,omitempty"`
	Section string `json:"section"`
	Text    string `json:"text"`
}

// UpdateRequest 用编辑后的完整条目列表替换记忆文件（按 revision 做 CAS）。
type UpdateRequest struct {
	Revision int64        `json:"revision"`
	Entries  []EntryInput `json:"entries"`
}

// SettingsRequest 只改记忆模式。
type SettingsRequest struct {
	Mode string `json:"mode"`
}

// DecisionRequest 是用户对待确认修订的采纳或拒绝。
type DecisionRequest struct {
	Decision string `json:"decision"`
}
