package commands

import "yingce/backend/internal/canvas/previs"

func (p *planner) canvasPrevisApplySceneUpdate(scene map[string]any, op canvasPrevisPatchOperation) (OperationSummary, error) {
	result, err := previs.ApplySceneUpdate(scene, previs.SceneUpdate{ID: op.ID, Title: op.Title, Background: op.Background, EnvironmentIntensity: op.EnvironmentIntensity, GridVisible: op.GridVisible, ActiveShotID: op.ActiveShotID}, previs.CommandIDs)
	if err != nil {
		if validation, ok := err.(*previs.ValidationError); ok && !validation.Plain {
			err = BadAuthRequest(validation.Error())
		}
		return OperationSummary{}, err
	}
	return OperationSummary{Operation: result.Operation, NodeID: result.ID, NodeTitle: result.Title, Summary: result.Summary}, nil
}
