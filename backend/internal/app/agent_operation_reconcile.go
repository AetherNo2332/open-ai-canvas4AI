package app

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	ao "yingce/backend/internal/agentorchestration"
	cp "yingce/backend/internal/creationplatform"
)

// reconcileAgentOperations projects the platform tasks behind one work's tool
// operations into reports. A submitted task reports progress once while it
// runs and exactly one final report when it reaches a terminal state; report
// identity is the task and its state, so redelivered reconciles are no-ops.
func (s *Service) reconcileAgentOperations(ownerID string) error {
	works, err := s.repo.AgentWorksWithSubmittedTasks(ownerID)
	if err != nil {
		return err
	}
	caller := cp.Caller{UserID: ownerID}
	for _, work := range works {
		current, err := s.repo.AgentWorkForOwner(ownerID, work.ID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if ao.WorkTerminal(current.Status) || current.ActiveAssignmentID == "" {
			continue
		}
		operations, err := s.repo.AgentOperationsForWork(ownerID, current.ID, 100)
		if err != nil {
			return err
		}
		assignment, err := s.repo.AgentAssignmentForOwner(ownerID, current.ActiveAssignmentID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		for _, operation := range operations {
			if !strings.HasPrefix(operation.OperationRef, "task:") {
				continue
			}
			taskID := strings.TrimPrefix(operation.OperationRef, "task:")
			snapshot, err := s.CreationPlatform().Task(context.Background(), caller, taskID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			report := agentTaskReport(snapshot)
			if report == nil {
				continue
			}
			if _, err := s.SubmitReport(ownerID, SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch,
				WorkVersion: current.Version, ReportID: report.reportID, Status: report.status, Summary: report.summary}); err != nil && !ao.IsCode(err, ao.CodeStaleAssignment) {
				return err
			}
		}
	}
	return nil
}

type agentTaskReportView struct {
	reportID string
	status   string
	summary  string
}

// agentTaskReport maps a platform task state onto one durable report: running
// work reports progress, a terminal task reports its outcome once.
func agentTaskReport(snapshot cp.TaskSnapshot) *agentTaskReportView {
	summary := "任务 " + snapshot.TaskID + "：" + string(snapshot.Execution)
	switch snapshot.Execution {
	case cp.ExecutionQueued, cp.ExecutionRunning:
		return &agentTaskReportView{reportID: "reconcile:" + snapshot.TaskID + ":progress", status: ao.ReportProgress,
			summary: "任务 " + snapshot.TaskID + " 正在执行（" + string(snapshot.Billing.Status) + "）"}
	case cp.ExecutionSucceeded:
		return &agentTaskReportView{reportID: "reconcile:" + snapshot.TaskID + ":completed", status: ao.ReportCompleted, summary: summary + "，结果已就绪"}
	case cp.ExecutionFailed, cp.ExecutionCancelled:
		return &agentTaskReportView{reportID: "reconcile:" + snapshot.TaskID + ":failed", status: ao.ReportFailed, summary: summary}
	}
	return nil
}
