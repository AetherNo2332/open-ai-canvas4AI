package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) DecideCloudAgentApproval(userID, id, approvalID, decision, reason string, mediaSettings ...*CloudAgentMediaSettings) error {
	defer s.wakeAgentTools()
	// Resource leases are a protection mechanism only. Expiry cleanup is safe to
	// run on the control-plane path and never changes the approval decision.
	if err := s.repo.ReleaseExpiredCloudAgentResourceLeases(time.Now().UTC()); err != nil {
		return err
	}
	settings, err := cloudAgentApprovalDecisionArgs(decision, reason, mediaSettings)
	if err != nil {
		return err
	}
	// New Pi runs intentionally have no root model task. Resolve identity through
	// the shared run reader so approvals work for both new execution rows and
	// legacy task-backed runs.
	if _, err := s.CloudAgentRun(userID, id); err != nil {
		return err
	}
	run, err := s.repo.CloudAgent(userID, id)
	if err != nil {
		return err
	}
	state, err := cloudAgentDecodeForExecution(run)
	if err != nil {
		return err
	}
	if previous, ok := state.Decisions[approvalID]; ok {
		if previous == decision && (settings == nil || state.DecisionSettings[approvalID] == creationHash(settings)) {
			return nil
		}
		return creationConflict("该审批已有不同决定")
	}
	if run.Status != "waiting_approval" || state.Approval == nil || state.Approval.ID != approvalID {
		return creationConflict("审批不存在或已过期")
	}
	// 运行中把权限收紧到只读后，卡片上的写入已不被允许：批准也不会执行，直接拒绝并说明，
	// 不让用户先看到“已批准”再看到失败。卡片仍在，用户可改回权限后再批准，或选择拒绝。
	if decision == "approve" && !cloudAgentCallPermitted(&state, state.Approval.Call) {
		return &kernel.AppError{Status: 409, Code: 409, Reason: kernel.ReasonAgentApprovalNotPermitted, Message: "当前执行权限不允许这次写入；请先切换权限后再批准，或拒绝本次操作"}
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.repo.MutateCloudAgent(userID, id, run.Revision, func(current *model.CloudAgentExecution, repo *repository.Repository) error {
		if settings != nil {
			if err := s.updateCloudAgentMediaApproval(repo, run, &state, *settings); err != nil {
				return err
			}
		}
		state.Approval.Decision = decision
		state.Approval.Reason = reason
		state.Decisions[approvalID] = decision
		if settings != nil {
			if state.DecisionSettings == nil {
				state.DecisionSettings = map[string]string{}
			}
			state.DecisionSettings[approvalID] = creationHash(settings)
		}
		if decision == "approve" && state.Approval.Prepared != nil {
			if state.DecisionPreparedHashes == nil {
				state.DecisionPreparedHashes = map[string]string{}
			}
			state.DecisionPreparedHashes[approvalID] = state.Approval.Prepared.Hash
		}
		if decision == "reject" {
			// Rejection is a user control-plane decision, not a failed tool
			// invocation. Make it terminal before the scheduler can advance the
			// pending call; no tool result, canvas mutation, generation task or
			// follow-up model request may be produced from this decision.
			current.Status = "rejected"
			current.FailureMessage = ""
			state.Approval = nil
			state.MediaBatch = nil
			state.event(id, "approval_decided", map[string]any{
				"approvalId": approvalID,
				"decision":   decision,
				"reason":     reason,
				"text":       "已拒绝本次生成，草稿节点仍保留在画布中；未提交任务、未产生扣费。你可以继续编辑后重新申请。",
			})
			if err := repo.ReleaseCloudAgentResourceLeases(userID, approvalID); err != nil {
				return err
			}
			return cloudAgentSave(current, &state)
		}
		current.Status = "running"
		payload := map[string]any{"approvalId": approvalID, "decision": decision, "arguments": json.RawMessage(state.Approval.Call.Function.Arguments), "preview": state.Approval.Preview, "modelName": state.Approval.ModelName}
		if state.Approval.Prepared != nil {
			payload["preparedHash"] = state.Approval.Prepared.Hash
			payload["generationId"] = state.Approval.Prepared.GenerationID
		}
		state.event(id, "approval_decided", payload)
		return cloudAgentSave(current, &state)
	})
}

func (s *Service) CancelCloudAgent(ctx context.Context, userID, id string) error {
	// 取消是控制面操作：即使运行的用户可见 runtime blob 已损坏也必须可用，
	// 所以先用带 user_id 条件的 execution 查询授权，不先解析 runtime。
	//
	// 但**不能**再以根任务作为唯一授权入口：阶段 2 之后的新 run 由 Go 直接创建、
	// 没有根任务行，按任务授权会让它们**完全无法取消** —— 用户只能看着它跑完，
	// 或者等看门狗超时判停。repo.CloudAgent 自身按 user_id 归属查询；仅旧版尚无
	// execution 行时才验证用户拥有的根任务，再通过正常读取路径引导创建 execution。
	run, err := s.loadCloudAgentForCancel(userID, id)
	if err != nil {
		return err
	}
	run, err = s.pauseAgentMessagesForStop(userID, run)
	if err != nil {
		return err
	}
	id = run.ID
	if run.Status == "completed" || ((run.Status == "failed" || run.Status == "cancelled") && !run.CleanupPending) {
		return nil
	}
	if run.Status != "failed" && run.Status != "cancelled" {
		if err := s.persistCloudAgentCancellation(userID, id, run); err != nil {
			return err
		}
	}
	latest, err := s.repo.CloudAgent(userID, id)
	if err != nil {
		return err
	}
	// Persist the run stop first so concurrent tool calls cannot enqueue new work.
	if err := s.cancelDynamicSubagents(ctx, userID, id); err != nil {
		return err
	}
	if err := s.finishCloudAgentCleanup(ctx, latest); err != nil {
		// A concurrent cleaner may still be finishing. Confirm durable cancellation
		// intent and acknowledge it; the watchdog retains any pending cleanup.
		if errors.Is(err, repository.ErrCreationConflict) {
			if current, readErr := s.repo.CloudAgent(userID, id); readErr == nil && current.Status == "cancelled" {
				return nil
			}
		}
		return err
	}
	return nil
}

// cloudAgentApprovalDecisionArgs validates the decision, reason and optional
// media settings before any run state is read.
func cloudAgentApprovalDecisionArgs(decision, reason string, mediaSettings []*CloudAgentMediaSettings) (*CloudAgentMediaSettings, error) {
	var settings *CloudAgentMediaSettings
	if len(mediaSettings) > 1 {
		return nil, BadAuthRequest("只能提交一组生成参数")
	}
	if len(mediaSettings) == 1 {
		settings = mediaSettings[0]
	}
	if settings != nil && decision != "approve" {
		return nil, BadAuthRequest("仅批准生成时可修改生成参数")
	}
	if decision != "approve" && decision != "reject" {
		return nil, BadAuthRequest("无效审批决定")
	}
	if len(reason) > 2000 {
		return nil, BadAuthRequest("审批理由过长")
	}
	return settings, nil
}

// persistCloudAgentCancellation records the cancel intent (status, cleanup flag
// and the first run_cancelled event) and treats a CAS conflict as success only
// when a reread confirms the run is already cancelled.
func (s *Service) persistCloudAgentCancellation(userID, id string, run *model.CloudAgentExecution) error {
	// Persist intent independently of the transcript. Retrying also repairs
	// legacy cancelled rows that crashed before cancelling their children.
	state, decodeErr := cloudAgentDecode(run)
	err := s.repo.MutateCloudAgent(userID, id, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		firstCancellation := current.Status != "cancelled"
		current.Status = "cancelled"
		current.CleanupPending = true
		if decodeErr == nil {
			current.CanvasID, current.ActiveTaskID, current.MediaTaskID = state.Request.CanvasID, state.ActiveTaskID, cloudAgentPendingGenerationIndexID(&state)
			if firstCancellation {
				state.event(id, "run_cancelled", map[string]any{"source": "user_request", "activeTaskId": state.ActiveTaskID, "mediaTaskId": state.MediaTaskID, "text": "用户取消接口已接收请求，正在取消关联任务"})
				if saveErr := cloudAgentSave(current, &state); saveErr != nil {
					// Cancellation must still work if the transcript is oversized.
					log.Printf("[cloud-agent] cancellation event unavailable for run %s: checkpoint rejected", id)
				}
			}
		} else {
			log.Printf("[cloud-agent] cancellation event unavailable for run %s: invalid transcript", id)
		}
		return nil
	})
	if err != nil {
		// 取消必须幂等：UI 双击、客户端重试、网络重发都会走到这里，而并发取消
		// 必然让其中一方撞上 CAS 冲突。金额侧本来是安全的（预留只释放一次），
		// 但把冲突当失败会让用户看到一次"取消失败"——而运行其实已经取消了。
		//
		// 只在"重新读取确认它确实已是 cancelled"时吞掉冲突；其它来源的冲突
		// （例如并发推进改动了别的字段）如实报错，不掩盖真实竞态。
		if !errors.Is(err, repository.ErrCreationConflict) {
			return err
		}
		current, readErr := s.repo.CloudAgent(userID, id)
		if readErr != nil {
			return readErr
		}
		if current.Status != "cancelled" {
			return err
		}
	}
	return nil
}

// loadCloudAgentForCancel reads the execution row by owner. Legacy root tasks
// may not have an execution row yet: verify task ownership and operation before
// using the normal read path, which validates the deterministic task identity
// and creates the execution row.
func (s *Service) loadCloudAgentForCancel(userID, id string) (*model.CloudAgentExecution, error) {
	run, err := s.repo.CloudAgent(userID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		task, taskErr := s.repo.TaskForUser(userID, id)
		if errors.Is(taskErr, gorm.ErrRecordNotFound) {
			return nil, kernel.NotFound("Agent 运行不存在")
		}
		if taskErr != nil {
			return nil, taskErr
		}
		if task.Operation != cloudAgentOperation {
			return nil, kernel.NotFound("Agent 运行不存在")
		}
		if _, err = s.CloudAgentRun(userID, id); err != nil {
			return nil, err
		}
		run, err = s.repo.CloudAgent(userID, id)
	}
	return run, err
}
