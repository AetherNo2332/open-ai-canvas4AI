package app

import (
	"encoding/json"
	"yingce/backend/internal/model"
)

func (s *Service) enrichAssetCanvasOrigins(userID string, assets []model.Asset) error {
	canvasIDs, taskIDs := []string{}, []string{}
	documents := make([]map[string]any, len(assets))
	for index, asset := range assets {
		if err := json.Unmarshal([]byte(asset.PayloadJSON), &documents[index]); err != nil {
			continue
		}
		metadata, _ := documents[index]["metadata"].(map[string]any)
		if id, ok := metadata["canvasId"].(string); ok && id != "" {
			canvasIDs = append(canvasIDs, id)
		}
		if id, ok := metadata["taskId"].(string); ok && id != "" {
			taskIDs = append(taskIDs, id)
		}
	}
	origins, err := s.repo.AssetCanvasOrigins(userID, canvasIDs, taskIDs)
	if err != nil {
		return err
	}
	for index, document := range documents {
		if document == nil {
			continue
		}
		metadata, ok := document["metadata"].(map[string]any)
		if !ok {
			continue
		}
		canvasID, _ := metadata["canvasId"].(string)
		taskID, _ := metadata["taskId"].(string)
		origin, found := origins["canvas:"+canvasID]
		if !found {
			origin, found = origins["task:"+taskID]
		}
		if !found {
			delete(metadata, "canvasName")
		} else {
			metadata["canvasId"], metadata["canvasName"] = origin.CanvasID, origin.CanvasName
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return err
		}
		assets[index].PayloadJSON = string(encoded)
	}
	return nil
}
