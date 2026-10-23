package service

import (
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/model"
)

type (
	AgentConversationMessageRequest = app.AgentConversationMessageRequest
	AgentMessageReceipt             = app.AgentMessageReceipt
	AgentMessageQueueView           = app.AgentMessageQueueView
	AgentQueuedMessageUpdate        = app.AgentQueuedMessageUpdate
	AgentMessageQueueUpdate         = app.AgentMessageQueueUpdate
	AgentUISetting                  = model.AgentUISetting
)
