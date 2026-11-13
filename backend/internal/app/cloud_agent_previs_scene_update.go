package app

import "yingce/backend/internal/canvas/previs"

func cloudAgentPrevisApplySceneUpdate(scene map[string]any, op cloudAgentPrevisPatchOperation) (cloudAgentApprovalPreviewItem, error) {
	result, err := previs.ApplySceneUpdate(scene, previs.SceneUpdate{ID: op.ID, Title: op.Title, Background: op.Background, EnvironmentIntensity: op.EnvironmentIntensity, GridVisible: op.GridVisible, ActiveShotID: op.ActiveShotID}, previs.AgentIDs)
	if err != nil {
		if validation, ok := err.(*previs.ValidationError); ok && !validation.Plain {
			err = BadAuthRequest(validation.Error())
		}
		return cloudAgentApprovalPreviewItem{}, err
	}
	return cloudAgentApprovalPreviewItem{Operation: result.Operation, NodeID: result.ID, NodeTitle: result.Title, Summary: result.Summary}, nil
}
