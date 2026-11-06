package app

import (
	"encoding/json"
	"time"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
)

// deliverAgentOutbox delivers pending items of one owner into the target
// Sessions' histories, oldest first. It is safe to run repeatedly and after a
// restart: each item lands once, keyed by its outbox ID.
func (s *Service) deliverAgentOutbox(ownerID string, limit int) (int, error) {
	items, err := s.repo.PendingAgentOutbox(ownerID, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, item := range items {
		message, err := s.outboxMessage(item)
		if err != nil {
			return delivered, err
		}
		if err := s.repo.DeliverAgentOutbox(item, message, time.Now()); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

// outboxMessage renders an item as a history message. A user's direct input
// stays the user's words; everything else is a system notice that carries
// facts, not authority.
func (s *Service) outboxMessage(item model.AgentOutbox) (model.AgentSessionMessage, error) {
	var payload map[string]any
	_ = json.Unmarshal([]byte(item.Payload), &payload)
	message := model.AgentSessionMessage{ID: newID(), MessageID: "outbox:" + item.ID, Kind: as.MessageKindMessage, Actor: as.ActorSystem, Role: "user"}
	switch item.Kind {
	case "work_input":
		input, err := s.repo.AgentWorkInputByID(item.OwnerID, stringValue(payload["inputId"]))
		if err != nil {
			return model.AgentSessionMessage{}, err
		}
		message.Actor, message.Body = input.Actor, input.Text
	case "assignment_started":
		message.Body = "新的派工：" + stringValue(payload["handoff"])
	case "report":
		message.Body = "子工作报告（" + stringValue(payload["status"]) + "）：" + stringValue(payload["summary"])
	case "user_input":
		message.Body = "用户直接补充了子工作的要求，请读取该工作的最新输入。"
	default:
		message.Body = "工作状态已更新。"
	}
	return message, nil
}
