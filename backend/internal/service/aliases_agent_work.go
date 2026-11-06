package service

import "yingce/backend/internal/app"

// Agent 服务的工作、派工、审批与消息类型由 internal/app 定义；这里只为 handler 再导出。
type (
	StartWorkRequest          = app.StartWorkRequest
	WorkView                  = app.WorkView
	WorkControlRequest        = app.WorkControlRequest
	ReportView                = app.ReportView
	ApprovalView              = app.ApprovalView
	DecideApprovalRequest     = app.DecideApprovalRequest
	SendSessionMessageRequest = app.SendSessionMessageRequest
	SessionMessageReceipt     = app.SessionMessageReceipt
	SessionMessagePage        = app.SessionMessagePage
)
