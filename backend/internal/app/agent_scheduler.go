package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"log"
	"time"
)

type PiRuntimePhaseRequest struct {
	Phase  string `json:"phase"`
	Kind   string `json:"kind"`
	WaitID string `json:"waitId"`
	Reason string `json:"reason"`
}

func (s *Service) PiRuntimePhase(userID, runID, owner string, input PiRuntimePhaseRequest) error {
	run, err := s.piAgentLeaseRow(userID, runID, owner, false)
	if err != nil {
		return err
	}
	if run.Status == "waiting_approval" && input.Phase == "waiting_tool" {
		input.Phase = "waiting_approval"
		input.Kind = "approval"
		input.Reason = "等待用户审批"
		var state struct {
			Approval *struct {
				ID string `json:"id"`
			} `json:"approval"`
		}
		if json.Unmarshal([]byte(run.StateJSON), &state) == nil && state.Approval != nil {
			input.WaitID = state.Approval.ID
		}
	}
	switch input.Phase {
	case "ready", "advancing", "waiting_model", "waiting_tool", "waiting_approval", "waiting_compaction", "waiting_resource", "retry_delay":
	default:
		return BadAuthRequest("Invalid Agent runtime phase")
	}
	if len(input.Kind) > 32 || len(input.WaitID) > 240 || len(input.Reason) > 160 {
		return BadAuthRequest("Invalid Agent wait reference")
	}
	workerID, epoch, _, err := parsePiAgentLeaseOwner(owner)
	if err != nil {
		return err
	}
	if err := s.repo.SetAgentPhase(userID, runID, workerID, epoch, input.Phase, input.Kind, input.WaitID, input.Reason); err != nil {
		return kernel.AgentLeaseLost("Agent runtime phase lease conflict")
	}
	return nil
}

func (s *Service) PiWakeEvents(after int64) ([]model.AgentWakeEvent, error) {
	return s.repo.AgentWakeEvents(after, 128)
}

func (s *Service) PiCapacity(owner string, active, capacity int) error {
	if owner == "" || len(owner) > 80 || active < 0 || capacity < 1 || capacity > 64 || active > capacity {
		return BadAuthRequest("Invalid Agent capacity")
	}
	return s.repo.AgentCapacity(owner, active, capacity)
}

func (s *Service) PiControl(userID, runID, owner string) (map[string]any, error) {
	run, err := s.piAgentLeaseRow(userID, runID, owner, false)
	if err != nil {
		if terminalRun, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return map[string]any{"status": terminalRun.Status}, nil
		}
		return nil, err
	}
	var control struct {
		PendingInterjections []cloudAgentInterjection     `json:"pendingInterjections"`
		ContextCompaction    *cloudAgentContextCompaction `json:"contextCompaction"`
	}
	if err := json.Unmarshal([]byte(run.StateJSON), &control); err != nil {
		return nil, err
	}
	pending := []PiPendingInterjection{}
	for _, item := range control.PendingInterjections {
		pending = append(pending, PiPendingInterjection{ID: item.ID, Text: item.Text, Source: item.Source, CreatedAt: item.CreatedAt})
	}
	var compaction *PiPendingContextCompaction
	if c := control.ContextCompaction; c != nil && c.PiOperationID != "" {
		compaction = &PiPendingContextCompaction{OperationID: c.PiOperationID, SessionRevision: c.PiSessionRevision, ActiveLeafID: c.PiSourceLeafID, Reason: c.PiReason, WillRetry: c.PiWillRetry, TokensBefore: c.PiTokensBefore}
	}
	return map[string]any{"status": run.Status, "pendingInterjections": pending, "pendingContextCompaction": compaction}, nil
}

func (s *Service) PiRelease(userID, runID, owner string) error {
	if _, err := s.piAgentLeasedRun(userID, runID, owner); err != nil {
		return err
	}
	workerID, epoch, _, err := parsePiAgentLeaseOwner(owner)
	if err != nil {
		return err
	}
	return s.repo.ReleasePiAgentLease(userID, runID, workerID, epoch)
}

func (s *Service) PiToolAdvanceAsync(userID, runID, owner, taskID, callID string) (*PiToolReceipt, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		if _, terminal := s.piTerminalRunAfterLeaseFailure(userID, runID, owner); terminal {
			return &PiToolReceipt{CallID: callID, Terminated: true}, nil
		}
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if taskID == "" || state.PiToolBatchTaskID != taskID || !piToolBatchContainsCall(state.Calls, callID) {
		return nil, kernel.Forbidden("Invalid Agent tool operation identity")
	}
	if receipt := piToolReceipt(&state, taskID, callID); receipt != nil {
		return receipt, nil
	}
	if cloudAgentRunTerminal(run.Status) {
		return &PiToolReceipt{CallID: callID, Terminated: true}, nil
	}
	if state.CallIndex >= len(state.Calls) || state.Calls[state.CallIndex].ID != callID {
		return nil, kernel.Forbidden("Invalid Agent tool operation order")
	}
	if run.WaitKind == "subagents" && run.WaitID == callID {
		return &PiToolReceipt{CallID: callID, Pending: true, Suspended: true}, nil
	}
	operationID := fmt.Sprintf("ato_%x", sha256.Sum256([]byte(runID+"\x00"+taskID+"\x00"+callID)))
	operation := model.AgentToolOperation{ID: operationID, UserID: userID, RunID: runID, TaskID: taskID, CallID: callID, Status: "queued"}
	if err := s.repo.PrepareAgentToolOperation(&operation); err != nil {
		return nil, err
	}
	if operation.Status == "failed" {
		return nil, kernel.BadAuthRequest("Agent 工具执行失败：" + operation.Error)
	}
	s.wakeAgentTools()
	return &PiToolReceipt{CallID: callID, Pending: true, OperationID: operationID}, nil
}

func (s *Service) wakeAgentTools() {
	if s == nil || s.agentToolWake == nil {
		return
	}
	select {
	case s.agentToolWake <- struct{}{}:
	default:
	}
}

func (s *Service) startAgentToolOperations() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		slots := make(chan struct{}, 16)
		for {
			if ctx.Err() != nil {
				return
			}
			if !s.IsDraining() {
				operations, err := s.repo.AgentToolOperations(64)
				if err != nil {
					log.Printf("agent tool recovery: %v", err)
				}
				for _, operation := range operations {
					if len(slots) >= cap(slots) {
						break
					}
					// Approval decisions wake this operation through durable recovery without
					// holding a tool worker while a human is deciding.
					run, err := s.repo.CloudAgent(operation.UserID, operation.RunID)
					if err != nil {
						continue
					}
					if run.Status == "waiting_approval" {
						continue
					}
					owner := s.workerID + ":" + newID()
					claimed, err := s.repo.ClaimAgentToolOperation(operation.ID, owner)
					if err != nil || !claimed {
						continue
					}
					slots <- struct{}{}
					op := operation
					if !s.runWorkerTask(func() {
						defer func() { <-slots }()
						status, message := "succeeded", ""
						current, err := s.repo.CloudAgent(op.UserID, op.RunID)
						if err == nil && !cloudAgentRunTerminal(current.Status) {
							session, _, loadErr := s.repo.CloudAgentPiSession(op.UserID, firstNonEmpty(current.ConversationID, current.ID))
							if loadErr != nil {
								err = loadErr
							} else if current.LeaseOwner == "" || session.LeaseExpiresAt == nil || session.LeaseExpiresAt.Before(time.Now()) {
								status = "pending"
							} else {
								// Renew the operation separately from the Agent session lease.
								done := make(chan struct{})
								go func() {
									tick := time.NewTicker(15 * time.Second)
									defer tick.Stop()
									for {
										select {
										case <-done:
											return
										case <-tick.C:
											_ = s.repo.RenewAgentToolOperation(op.ID, owner)
										}
									}
								}()
								receipt, advanceErr := s.PiToolAdvance(op.UserID, op.RunID, fmt.Sprintf("%s@%d", current.LeaseOwner, session.LeaseEpoch), op.TaskID, op.CallID)
								close(done)
								err = advanceErr
								if receipt != nil && receipt.Pending {
									status = "pending"
								}
							}
						}
						if err != nil {
							// Lease loss/revision conflicts remain recoverable. Deterministic errors
							// are reported by the next Agent receipt read.
							status = "pending"
							message = "工具执行暂时失败，等待恢复"
							var appError *kernel.AppError
							if errors.As(err, &appError) && appError.Status >= 400 && appError.Status < 500 && appError.Status != 409 && appError.Status != 429 && appError.Reason != kernel.ReasonAgentLeaseLost {
								status = "failed"
								message = appError.Message
							}
						}
						if finishErr := s.repo.FinishAgentToolOperation(op, owner, status, message); finishErr != nil {
							log.Printf("agent tool completion: %v", finishErr)
						}
					}) {
						<-slots
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.agentToolWake:
			}
		}
	})
}
