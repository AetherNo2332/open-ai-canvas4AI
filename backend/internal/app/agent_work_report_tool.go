package app

import (
	"encoding/json"
	"fmt"
	"strings"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
)

// executorWorkReportSpec is the one shared executor tool: every business
// Session can report its own work, and the registry keeps it out of the
// per-application tool ownership map.
func executorWorkReportSpec() as.ToolSpec {
	return as.ToolSpec{Name: "report_work", Description: "向总控报告本子工作的结果：status(completed|failed|progress|needs_user) 与 summary",
		Parameters: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"},"summary":{"type":"string"}},"required":["status","summary"]}`)}
}

// executeWorkReportTool lets an executor report its own work to the supervisor.
// The report identity comes from the operation, so a replayed turn reports once;
// the assignment and work version are resolved server-side.
func (s *Service) executeWorkReportTool(call as.ToolInvocation) (as.ToolResult, error) {
	var args struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Summary) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "报告需要状态与摘要")
	}
	status := args.Status
	switch status {
	case ao.ReportCompleted, ao.ReportFailed, ao.ReportProgress, ao.ReportNeedsUser:
	default:
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "报告状态无效")
	}
	if call.WorkItemID == "" {
		return as.ToolResult{}, as.NewError(as.CodeForbidden, "该会话没有绑定工作")
	}
	work, err := s.repo.AgentWorkForOwner(call.OwnerID, call.WorkItemID)
	if err != nil {
		return as.ToolResult{}, workNotFound(err)
	}
	if work.ActiveAssignmentID == "" {
		return as.ToolResult{}, as.NewError(as.CodeStaleAssignment, "工作没有有效派工")
	}
	assignment, err := s.repo.AgentAssignmentForOwner(call.OwnerID, work.ActiveAssignmentID)
	if err != nil {
		return as.ToolResult{}, workNotFound(err)
	}
	reportID := "tool:" + agentApprovalCallIdentity(call)
	if _, err := s.SubmitReport(call.OwnerID, SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch,
		WorkVersion: work.Version, ReportID: reportID, Status: status, Summary: strings.TrimSpace(args.Summary)}); err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("已向总控报告：%s。", status), Operation: work.ID}, nil
}
