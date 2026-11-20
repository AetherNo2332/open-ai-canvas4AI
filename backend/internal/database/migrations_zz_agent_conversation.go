package database

import (
	"encoding/json"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

func init() {
	schemaMigrations = append(schemaMigrations, migration{
		version: 109, name: "agent_conversation_scope", checksum: "agent-conversation-scope-v109-20261118",
		apply: migrateAgentConversationScope,
	})
}

func migrateAgentConversationScope(tx *gorm.DB) error {
	if err := tx.AutoMigrate(&model.CloudAgentExecution{}, &model.CloudAgentPiSession{}); err != nil {
		return err
	}
	var runs []model.CloudAgentExecution
	if err := tx.Select("id", "user_id", "canvas_id", "state_json").FindInBatches(&runs, 200, func(batch *gorm.DB, _ int) error {
		for _, run := range runs {
			binding := agentConversationBindingFromRequest(run)
			if err := tx.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).UpdateColumns(map[string]any{
				"scope_type": binding.ScopeType, "scope_id": binding.ScopeID, "agent_definition_id": binding.AgentDefinitionID,
				"agent_context_version": binding.AgentContextVersion,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	}).Error; err != nil {
		return err
	}
	if err := tx.Exec(`UPDATE cloud_agent_executions AS r
		SET scope_type = '', scope_id = '', agent_definition_id = '', agent_context_version = 0
		WHERE EXISTS (SELECT 1 FROM cloud_agent_executions x
		WHERE x.user_id = r.user_id AND COALESCE(NULLIF(x.conversation_id, ''), x.id) = COALESCE(NULLIF(r.conversation_id, ''), r.id)
		AND (x.scope_type <> r.scope_type OR x.scope_id <> r.scope_id OR x.agent_definition_id <> r.agent_definition_id))`).Error; err != nil {
		return err
	}
	var sessions []model.CloudAgentPiSession
	if err := tx.FindInBatches(&sessions, 200, func(batch *gorm.DB, _ int) error {
		for _, session := range sessions {
			var run model.CloudAgentExecution
			if err := tx.Select("scope_type", "scope_id", "agent_definition_id", "agent_context_version").
				Where("id = ? AND user_id = ? AND (conversation_id = ? OR (conversation_id = '' AND id = ?))",
					session.ActiveRunID, session.UserID, session.ConversationID, session.ConversationID).Find(&run).Error; err != nil {
				return err
			}
			updates := map[string]any{
				"scope_type": run.ScopeType, "scope_id": run.ScopeID, "agent_definition_id": run.AgentDefinitionID,
				"agent_context_version": run.AgentContextVersion,
			}
			if run.AgentContextVersion > 0 && run.ScopeType != "canvas" {
				var header map[string]any
				if err := json.Unmarshal([]byte(session.HeaderJSON), &header); err != nil {
					return err
				}
				header["cwd"] = run.ScopeType + "://" + run.ScopeID
				raw, err := json.Marshal(header)
				if err != nil {
					return err
				}
				updates["header_json"] = string(raw)
			}
			if err := tx.Model(&session).UpdateColumns(updates).Error; err != nil {
				return err
			}
		}
		return nil
	}).Error; err != nil {
		return err
	}
	return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_cloud_agent_scope_directory
		ON cloud_agent_executions (user_id, scope_type, scope_id, agent_definition_id, created_at DESC, id DESC)`).Error
}

func agentConversationBindingFromRequest(run model.CloudAgentExecution) model.AgentConversationBinding {
	type scope struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	var state struct {
		Request struct {
			CanvasID     string `json:"canvasId"`
			Scope        *scope `json:"scope"`
			DefinitionID string `json:"agentDefinitionId"`
		} `json:"request"`
		Context *struct {
			Version int `json:"version"`
			Scope   struct {
				UserID   string `json:"userId"`
				Business scope  `json:"business"`
			} `json:"scope"`
		} `json:"context"`
		Capabilities *struct {
			DefinitionID string `json:"definitionId"`
			BusinessType string `json:"businessType"`
		} `json:"capabilities"`
	}
	if json.Unmarshal([]byte(run.StateJSON), &state) != nil || state.Request.CanvasID != run.CanvasID {
		return model.AgentConversationBinding{}
	}
	business := state.Request.Scope
	if business == nil {
		business = &scope{Type: "canvas", ID: state.Request.CanvasID}
	}
	if business.ID == "" || business.Type == "" ||
		(business.Type == "canvas" && business.ID != run.CanvasID) ||
		(business.Type != "canvas" && run.CanvasID != "") {
		return model.AgentConversationBinding{}
	}
	binding := model.AgentConversationBinding{ScopeType: business.Type, ScopeID: business.ID, AgentDefinitionID: state.Request.DefinitionID}
	if state.Capabilities != nil {
		caps := state.Capabilities
		if caps.BusinessType != business.Type || caps.DefinitionID == "" ||
			(binding.AgentDefinitionID != "" && binding.AgentDefinitionID != caps.DefinitionID) {
			return model.AgentConversationBinding{}
		}
		binding.AgentDefinitionID = caps.DefinitionID
		if state.Context != nil && state.Context.Scope.UserID == run.UserID && state.Context.Scope.Business == *business {
			binding.AgentContextVersion = state.Context.Version
		}
	}
	if binding.AgentDefinitionID == "" {
		binding.AgentDefinitionID = map[string]string{"canvas": "canvas.default", "drama": "drama.analysis"}[business.Type]
	}
	return binding
}
