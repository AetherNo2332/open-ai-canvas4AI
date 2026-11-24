package app

import (
	"encoding/json"
	"time"

	"yingce/backend/internal/kernel"
)

// PendingCloudAgentDirectorPreviews is a user-owned cross-page index. It never
// broadens canvas access: every row must still resolve the current owner and
// the registered canvas Agent definition before it is returned.
func (s *Service) PendingCloudAgentDirectorPreviews(userID string) (*CloudAgentPendingDirectorPreviewPage, error) {
	if userID == "" {
		return nil, kernel.Unauthorized("请先登录")
	}
	runs, err := s.repo.PendingDirectorPreviewRuns(userID, 50)
	if err != nil {
		return nil, err
	}
	page := &CloudAgentPendingDirectorPreviewPage{Items: []CloudAgentPendingDirectorPreview{}}
	for index := range runs {
		run := &runs[index]
		if run.ScopeType != "canvas" || run.CanvasID == "" {
			continue
		}
		var checkpoint struct {
			DirectorPreview *cloudAgentDirectorPreview `json:"directorPreview"`
		}
		if err := json.Unmarshal([]byte(run.StateJSON), &checkpoint); err != nil {
			continue
		}
		preview := cloudAgentPendingDirectorPreviewFields(run, checkpoint.DirectorPreview)
		if preview == nil || !time.Now().UTC().Before(preview.Deadline) {
			continue
		}
		if _, err := s.agentConversationBinding(userID, AgentConversationSelector{
			Scope:             AgentBusinessScope{Type: "canvas", ID: run.CanvasID},
			AgentDefinitionID: run.AgentDefinitionID,
		}); err != nil {
			continue
		}
		page.Items = append(page.Items, *preview)
	}
	return page, nil
}
