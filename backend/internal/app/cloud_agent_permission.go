package app

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 运行中切换执行权限（2026-10-30 用户拍板）：
//   - 放宽和收紧都从**下一次工具调用**生效：运行时每次执行工具都重读
//     state.Request.PermissionMode，这里只需要改运行态并在同一事务里记账；
//   - 模型通过注入的系统通知得知变化，**不改写系统提示**：系统提示与 prompt cache key
//     始终按开局权限计算（cloudAgentCacheRequest），换权限只让工具段缓存失效一次；
//   - 正在等待的审批卡保持原样，仍需用户明确批准或拒绝；只有之后的写入按新权限处理；
//   - 权限切换仅作用于本轮已经准入的工具，目录与预算不随权限改变。
const (
	cloudAgentPermissionSource = "permission"
	// 权限变更历史只记最近这些，用于 requestId 幂等；有界，不随运行时长增长。
	cloudAgentPermissionHistoryMemory = 32
)

// cloudAgentPermissionChange 一次已生效的权限切换。
type cloudAgentPermissionChange struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	At        time.Time `json:"at"`
	RequestID string    `json:"requestId"`
}

// CloudAgentPermissionResult 是切换接口的返回：当前生效的权限与本次是否真的改变了。
type CloudAgentPermissionResult struct {
	PermissionMode string `json:"permissionMode"`
	Changed        bool   `json:"changed"`
}

func cloudAgentValidPermissionMode(mode string) bool {
	return mode == "read_only" || mode == "request_approval" || mode == "auto"
}

func cloudAgentPermissionLabel(mode string) string {
	switch mode {
	case "read_only":
		return "只读"
	case "request_approval":
		return "请求审批"
	default:
		return "自动执行"
	}
}

// cloudAgentPermissionNoticeText 是注入给模型的系统通知正文。它是事实，不是用户的新任务。
func cloudAgentPermissionNoticeText(from, to string) string {
	prefix := "[系统通知] 用户已把执行权限从「" + cloudAgentPermissionLabel(from) + "」改为「" + cloudAgentPermissionLabel(to) + "」"
	switch to {
	case "read_only":
		return prefix + "：从现在起不能写入画布或生成，已提交的任务不受影响。这不是新的任务要求。"
	case "request_approval":
		return prefix + "：本轮已获准的写入和生成仍需用户审批；新增工具须开启新一轮。这不是新的任务要求。"
	default:
		return prefix + "：本轮已获准的写入可自动执行（生成仍受审批与积分预算限制）；新增工具须开启新一轮。这不是新的任务要求。"
	}
}

// cloudAgentStartPermissionMode 是开局权限：系统提示、执行上下文与缓存键都按它冻结。
func cloudAgentStartPermissionMode(state *cloudAgentRuntime) string {
	if state == nil {
		return ""
	}
	if state.StartPermissionMode != "" {
		return state.StartPermissionMode
	}
	return state.Request.PermissionMode
}

// cloudAgentCacheRequest 返回计算 prompt cache 身份用的请求：权限取开局值，
// 运行中切换权限不改变系统提示与缓存键。
func cloudAgentCacheRequest(state *cloudAgentRuntime) CloudAgentRequest {
	req := state.Request
	req.PermissionMode = cloudAgentStartPermissionMode(state)
	return req
}

func cloudAgentPermissionRequestSeen(state *cloudAgentRuntime, requestID string) bool {
	for _, change := range state.PermissionChanges {
		if change.RequestID == requestID {
			return true
		}
	}
	return false
}

// cloudAgentQueuePermissionNotice 排一条权限通知。下一步之前多次切换只保留一条：
// from 取模型最后知道的权限（尚未送达的旧通知的 from），to 取最终值；
// 切回模型已知的权限时撤掉通知。
func cloudAgentQueuePermissionNotice(state *cloudAgentRuntime, from, to, requestID string) {
	known := from
	kept := state.PendingInterjections[:0]
	for _, item := range state.PendingInterjections {
		if item.Source == cloudAgentPermissionSource {
			if item.PermissionFrom != "" {
				known = item.PermissionFrom
			}
			continue
		}
		kept = append(kept, item)
	}
	state.PendingInterjections = kept
	if known == to {
		return
	}
	state.PendingInterjections = append(state.PendingInterjections, cloudAgentInterjection{
		ID: "permission:" + requestID, Text: cloudAgentPermissionNoticeText(known, to), Source: cloudAgentPermissionSource,
		PermissionFrom: known, CreatedAt: time.Now(),
	})
}

// ChangeCloudAgentPermission 在运行中切换执行权限，下一次工具调用生效。
func (s *Service) ChangeCloudAgentPermission(userID, id, mode, requestID string) (*CloudAgentPermissionResult, error) {
	if !cloudAgentValidPermissionMode(mode) {
		return nil, BadAuthRequest("无效的 Agent 执行权限")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || !utf8.ValidString(requestID) || utf8.RuneCountInString(requestID) > 160 || strings.ContainsRune(requestID, 0) {
		return nil, BadAuthRequest("权限切换请求标识无效")
	}
	// 归属校验：不是本人的运行返回 404，而不是 CAS 冲突。
	if _, err := s.cloudAgentRunRefFor(userID, id); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < cloudAgentInterjectionAttempts; attempt++ {
		run, err := s.repo.CloudAgent(userID, id)
		if err != nil {
			return nil, err
		}
		state, err := cloudAgentDecode(run)
		if err != nil {
			return nil, err
		}
		if !agentRunPermissionPermitted(&state, mode) {
			return nil, kernel.Forbidden("该 Agent 配置仅允许只读运行")
		}
		if err := s.authorizeRegisteredAgentRun(run, &state); err != nil {
			return nil, err
		}
		if cloudAgentPermissionRequestSeen(&state, requestID) {
			return &CloudAgentPermissionResult{PermissionMode: state.Request.PermissionMode, Changed: true}, nil
		}
		if !cloudAgentAcceptsInterjection(run.Status) {
			return nil, &kernel.AppError{Status: 409, Code: 409, Reason: kernel.ReasonAgentRunFinished, Message: "本轮已经结束，权限将用于下一轮"}
		}
		if state.Subagent != nil {
			return nil, &kernel.AppError{Status: 409, Code: 409, Reason: kernel.ReasonAgentSubagentPermissionFixed, Message: "子代理权限在 spawn 时定型，不能切换执行权限"}
		}
		from := state.Request.PermissionMode
		if from == mode {
			return &CloudAgentPermissionResult{PermissionMode: mode}, nil
		}
		err = s.repo.MutateCloudAgent(userID, id, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			if state.StartPermissionMode == "" {
				state.StartPermissionMode = from
			}
			state.Request.PermissionMode = mode
			state.PermissionChanges = append(state.PermissionChanges, cloudAgentPermissionChange{From: from, To: mode, At: time.Now().UTC(), RequestID: requestID})
			if len(state.PermissionChanges) > cloudAgentPermissionHistoryMemory {
				state.PermissionChanges = state.PermissionChanges[len(state.PermissionChanges)-cloudAgentPermissionHistoryMemory:]
			}
			cloudAgentQueuePermissionNotice(&state, from, mode, requestID)
			state.event(id, "permission_changed", map[string]any{"from": from, "to": mode, "requestId": requestID})
			return cloudAgentSave(current, &state)
		})
		if errors.Is(err, repository.ErrCreationConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &CloudAgentPermissionResult{PermissionMode: mode, Changed: true}, nil
	}
	return nil, creationConflict("Agent 正在写入运行状态，请重试")
}
