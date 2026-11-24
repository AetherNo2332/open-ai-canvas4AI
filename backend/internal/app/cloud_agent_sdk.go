package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"yingce/backend/internal/kernel"
)

const AgentSDKContractVersion = "agent-sdk/v1"

func cloudAgentID(userID, key string) string {
	sum := sha256.Sum256([]byte(userID + "\x00" + key))
	return "ag" + hex.EncodeToString(sum[:16])
}

func cloudAgentFingerprint(req CloudAgentRequest, parent string) string {
	// Equivalent Canvas Scope and legacy payloads share a retry identity.
	if req.Scope != nil && req.Scope.Type == "canvas" {
		req.Scope = nil
	}
	data, _ := json.Marshal(struct {
		Request CloudAgentRequest
		Parent  string
	}{req, parent})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// AgentBusinessScope identifies a real business object; it never grants access.
type AgentBusinessScope struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type AgentRuleSnapshot struct {
	Source   string `json:"source"`
	Revision int64  `json:"revision"`
	Text     string `json:"text"`
}

func agentBusinessScope(req CloudAgentRequest) AgentBusinessScope {
	if req.Scope != nil {
		return *req.Scope
	}
	return AgentBusinessScope{Type: "canvas", ID: req.CanvasID}
}

func validateAgentBusinessScope(req *CloudAgentRequest) error {
	if req.Scope == nil {
		if req.DramaEpisodeID == "" {
			return nil
		}
		return kernel.BadAuthRequest("剧集分析需要明确的短剧范围")
	}
	if !validAgentScopedRequest(req) {
		err := kernel.BadAuthRequest("Agent 业务范围无效或与画布不一致")
		err.Reason = "agent_scope_invalid"
		return err
	}
	return nil
}

func validAgentScopedRequest(req *CloudAgentRequest) bool {
	if req.Scope.Type == "canvas" {
		return req.Scope.ID == req.CanvasID && req.DramaEpisodeID == ""
	}
	if req.CanvasID != "" || len(req.ContextScope) != 0 || len(req.FocusNodeIDs) != 0 || req.SubagentEnabled || req.SubagentWriteEnabled {
		return false
	}
	if req.Scope.Type == AgentPlatformBusinessType {
		return req.PermissionMode == "read_only" && len(req.SkillIDs) == 0 &&
			validateCloudAgentID(req.Scope.ID, "平台账号 ID", 80) == nil
	}
	if req.Scope.Type != "drama" {
		return validateCloudAgentID(req.Scope.Type, "业务类型", 40) == nil && req.DramaEpisodeID == ""
	}
	return req.PermissionMode == "read_only" && validateCloudAgentID(req.Scope.ID, "短剧项目 ID", 36) == nil &&
		(req.DramaEpisodeID == "" || validateCloudAgentID(req.DramaEpisodeID, "短剧集 ID", 36) == nil)
}

func agentSDKAdmissionEnabled(req CloudAgentRequest) error {
	scope := agentBusinessScope(req)
	if (scope.Type == "drama" || scope.Type == AgentPlatformBusinessType) && os.Getenv("CANVAS_AGENT_SDK_BUSINESS_DISABLED") == "true" {
		err := kernel.NewAppError(503, "短剧 Agent 新准入已暂停，已有运行仍可查看或取消")
		err.Reason = "agent_business_admission_disabled"
		return err
	}
	return nil
}

func agentTaskProjectID(req CloudAgentRequest) string {
	return agentBusinessScope(req).ID
}

func agentIdempotencyConflict(message string) error {
	err := kernel.NewAppError(409, message)
	err.Reason = "agent_idempotency_conflict"
	return err
}
