// 用户长期记忆文件：每个用户一份、有容量上限的条目列表，分“关于用户”和“工作笔记”两节。
// Agent 用 memory_update 自己增删改；默认直接生效、每次修改留修订可撤销，也可切到需确认或关闭。
// 每轮开始时冻结一份快照，作为有来源的数据消息放在当前用户消息前：不进入稳定 system 前缀，
// 也不构成权限、审批或收费授权。
//
// 文档行为（编辑规则、快照渲染与提醒、修订、审批、撤销、Agent 写入）已迁
// internal/agentmemory/document；app 保留 run 快照冻结/注入、工具事件适配与管理员跨用户聚合。

package app

import (
	"strings"

	"yingce/backend/internal/agentmemory"
	"yingce/backend/internal/agentmemory/document"
	"yingce/backend/internal/model"
)

const cloudAgentMemoryContextSource = "agent_memory_doc"

// 记忆文档的行为与类型归 document 所有；这些别名保持既有调用点与测试不变。
type (
	agentMemoryOperation = document.Operation
	AgentMemoryDocChange = document.Change
)

const (
	agentMemoryOutcomeApplied   = document.OutcomeApplied
	agentMemoryOutcomePending   = document.OutcomePending
	agentMemoryOutcomeUnchanged = document.OutcomeUnchanged
	agentMemoryCallsPerRun      = document.CallsPerRun
	agentMemoryReasonMaxRunes   = document.ReasonMaxRunes
	agentMemoryOpsMax           = document.OpsMax
	agentMemoryEntryMaxRunes    = document.EntryMaxRunes
)

var agentMemoryValidateText = agentmemory.ValidateText

// cloudAgentMemorySnapshot 是建 run 时冻结的记忆快照；本轮内 Agent 的修改只体现在工具结果里。
type cloudAgentMemorySnapshot struct {
	Source   string `json:"source,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Revision int64  `json:"revision,omitempty"`
	Text     string `json:"text,omitempty"`
}

// agentMemorySnapshotText 是文档规则包的兼容出口；app 只负责把冻结结果注入本轮消息。
func agentMemorySnapshotText(mode string, capacity int, entries []model.AgentMemoryEntry, nudge bool) string {
	return document.SnapshotText(mode, capacity, entries, nudge)
}

func agentMemoryNudge(prompt string) bool {
	return document.ShouldNudge(prompt)
}

// cloudAgentAttachMemorySnapshot 把冻结的记忆快照作为一条有来源的 user 数据消息插在当前用户
// 消息之前。放在消息里而不是系统提示：系统提示是跨运行的缓存前缀，记忆随用户整理而变。
// 快照只属于本轮：续轮历史由 TextHistory 重建，不带这条消息（见 cloudAgentContinuationHistory）。
func cloudAgentAttachMemorySnapshot(canonical *canonicalAgentRequest, memory cloudAgentMemorySnapshot) {
	if canonical == nil || strings.TrimSpace(memory.Text) == "" || len(canonical.Messages) == 0 {
		return
	}
	for _, message := range canonical.Messages {
		if stringField(message, cloudAgentContextSourceKey) == cloudAgentMemoryContextSource {
			return
		}
	}
	snapshot := map[string]any{"role": "user", "content": memory.Text, cloudAgentContextSourceKey: cloudAgentMemoryContextSource}
	last := canonical.Messages[len(canonical.Messages)-1]
	canonical.Messages = append(append(append([]map[string]any{}, canonical.Messages[:len(canonical.Messages)-1]...), snapshot), last)
}
