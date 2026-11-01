package app

import (
	"encoding/json"

	"yingce/backend/internal/model"
)

// cloudAgentExecutionOutput 只依赖运行的归属与 ID，不再要求一条根任务行。
//
// 迁移背景：阶段 2 之后的新 run 由 Go 直接创建（run + 预授权 + 不可变快照），
// **不再有根任务**，因此这里不能再从 *model.Task 取身份。旧 run 的调用方传
// 根任务的 ID 即可，语义完全不变（旧的 runID 就等于根任务 ID）。
func (s *Service) cloudAgentExecutionOutput(userID string, identity cloudAgentRunIdentity, initial cloudAgentState, options ...CloudAgentRunViewOptions) (*CloudAgentRun, error) {
	run, err := s.repo.CloudAgent(userID, identity.ID)
	if err != nil {
		return nil, err
	}
	state, stateErr := cloudAgentDecode(run)
	if stateErr != nil {
		// A terminal failed run must remain readable even if its durable runtime
		// blob was damaged. Do not invent permissions or approval state; expose
		// only the identity available from the original task input.
		state = cloudAgentRuntime{Request: initial.Request, ParentID: initial.ParentID, CreativeAnchor: initial.CreativeAnchor, Skills: initial.Skills, TaskIDs: []string{identity.ID}, Events: []CloudAgentEvent{}}
	}
	out := agentRunOutput(identity, initial)
	out.ConversationID = firstNonEmpty(run.ConversationID, run.ID)
	out.Status = run.Status
	out.RuntimePhase, out.WaitReason = run.RuntimePhase, run.WaitReason
	out.Revision, out.CleanupPending, out.FailureMessage = run.Revision, run.CleanupPending, run.FailureMessage
	out.UpdatedAt = run.UpdatedAt
	view := CloudAgentRunViewOptions{}
	if len(options) > 0 {
		view = options[0]
	}
	// 事件已全量落库，运行详情只返回一页：默认是尾部窗口，sinceSeq 只取增量。
	// 四个位置字段与 events 一起返回，客户端据此判断"是否还有更早的记录"。
	out.Events = s.cloudAgentRunEventsForView(userID, run, &state, view.SinceSeq, view.EventLimit, view.FromStart)
	// EventSeqBase 必须与真正返回的这一页对齐（eventLimit 把它收窄时也一样），
	// 不变量 events[i].seq == eventSeqBase + i + 1 才成立。页为空时退回窗口水位：
	// 那时没有"首条事件"，但仍然要能说明窗口在整条日志里的位置。
	out.EventSeqBase = state.EventSeqBase
	if len(out.Events) > 0 {
		out.EventSeqBase = out.Events[0].Seq - 1
	}
	out.EventCount = s.cloudAgentRunEventCount(userID, run, &state)
	if len(out.Events) > 0 {
		out.LatestSeq = out.Events[len(out.Events)-1].Seq
	}
	if view.SinceSeq == 0 && !view.FromStart && len(out.Events) < out.EventCount {
		out.EventsTruncated = true
	}
	out.Approval = state.Approval
	if cloudAgentRunTerminal(run.Status) {
		out.Approval = nil
	}
	out.Step = state.Step
	out.SkillRuntimeMode = firstNonEmpty(state.SkillRuntimeMode, cloudAgentSkillRuntimeLegacy)
	if stateErr == nil && state.ActiveTaskID != "" && (run.Status == "running" || run.Status == "queued") {
		active, err := s.repo.TaskForUser(userID, state.ActiveTaskID)
		if err != nil {
			return nil, err
		}
		if message := cloudAgentRunActiveMessage(&state, active); message != nil {
			out.ActiveMessage = message
		}
	}
	out.Skills = cloudAgentRunPublicSkills(state.Skills)
	orders, err := s.repo.BillingOrdersByTaskIDs(userID, state.TaskIDs)
	if err != nil {
		return nil, err
	}
	out.SpentCredits = cloudAgentRunSpentCredits(out.SpentCredits, orders)
	return out, nil
}

func cloudAgentRunActiveMessage(state *cloudAgentRuntime, active *model.Task) map[string]string {
	draft := state.ActiveTextDraft
	if draft == "" {
		// Legacy runs have no runtime draft; keep their task-level replay
		// value visible while new runs use the CAS-updated runtime source.
		draft = active.TextDraft
	}
	if draft == "" {
		return nil
	}
	return map[string]string{"messageId": active.ID, "text": draft}
}

func cloudAgentRunPublicSkills(skills []cloudAgentSkill) []cloudAgentSkill {
	out := make([]cloudAgentSkill, 0, len(skills))
	for _, skill := range skills {
		skill.Instruction = ""
		skill.Files = nil
		skill.NativeFiles = nil
		out = append(out, skill)
	}
	return out
}

// cloudAgentRunSpentCredits adds settled amounts onto spent. The orders map has
// no stable order, so it adds onto the caller's running total exactly as the
// original loop added onto out.SpentCredits.
func cloudAgentRunSpentCredits(spent float64, orders map[string]model.BillingOrder) float64 {
	for _, order := range orders {
		// AmountMicrocredits is the reservation/quote, not necessarily the amount
		// finally charged. A run can reserve 100000 microcredits and settle at
		// 100000 microcredits (= 0.1 credits), or be refunded altogether. Never
		// expose the reservation as spend, otherwise the Agent claims a charge
		// that did not happen and budget/usage copy diverges from billing.
		if order.Status == model.BillingStatusSettled {
			spent += float64(order.ActualAmountMicrocredits) / float64(CreditScale)
		}
	}
	return spent
}

func compactCloudAgentContext(request *canonicalAgentRequest, budget cloudAgentContextBudget) bool {
	if request == nil {
		return false
	}
	tokens, err := cloudAgentRequestEstimatedTokens(request)
	if err != nil || tokens < budget.CompactAtTokens {
		return false
	}
	// Retain the latest complete tool turn. Never remove call/result envelopes,
	// user instructions, call arguments or write receipts to fabricate a summary.
	cut := len(request.Messages) - 1
	for cut > 0 && stringField(request.Messages[cut], "role") == "tool" {
		cut--
	}
	changed := false
	for _, message := range request.Messages[:max(0, cut)] {
		if cloudAgentCompactToolMessage(message) {
			changed = true
		}
	}
	return changed
}

// cloudAgentCompactToolMessage replaces one historical tool result with its
// structured facts when that makes the message shorter.
func cloudAgentCompactToolMessage(message map[string]any) bool {
	if stringField(message, "role") != "tool" {
		return false
	}
	var result map[string]any
	if json.Unmarshal([]byte(stringField(message, "content")), &result) != nil || result["contextCompacted"] == true {
		return false
	}
	// Only omit re-readable bodies. Preserve IDs, errors, generation status,
	// approvals and all other structured facts verbatim.
	omitted := false
	if nodes, ok := result["nodes"].([]any); ok {
		if facts := cloudAgentCompactedNodeFacts(nodes); len(facts) > 0 {
			result["observedFacts"] = facts
		}
	}
	for _, key := range []string{"content", "nodes"} {
		if _, exists := result[key]; exists {
			delete(result, key)
			omitted = true
		}
	}
	if !omitted {
		return false
	}
	result["contextCompacted"] = true
	result["guidance"] = "历史读取正文已移出模型上下文；需要时重新读取。保留的历史状态不是当前状态，也不是执行授权，不得据此重复提交生成。"
	body, err := json.Marshal(result)
	if err != nil || len(body) >= len(stringField(message, "content")) {
		return false
	}
	message["content"] = string(body)
	return true
}

func cloudAgentCompactedNodeFacts(nodes []any) []map[string]any {
	facts := make([]map[string]any, 0, len(nodes))
	for _, value := range nodes {
		if node, ok := value.(map[string]any); ok {
			fact := map[string]any{"nodeId": node["id"]}
			for _, key := range []string{"generation", "generationDraft", "outputReference"} {
				if value, exists := node[key]; exists {
					fact[key] = value
				}
			}
			if len(fact) > 1 {
				facts = append(facts, fact)
			}
		}
	}
	return facts
}
