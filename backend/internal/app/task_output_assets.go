package app

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

// Read the registered identity instead of asking HTTP clients to derive hashes
// or create another asset when their local cache has not seen task completion.
func (s *Service) bindRegisteredTaskAssets(task *model.Task) error {
	if task.Status != model.TaskStatusSucceeded || task.ResultJSON == "" {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		return err
	}
	var representations []model.AssetRepresentation
	representationsLoaded := false
	bind := func(media map[string]any, index int) error {
		delete(media, "assetId")
		key, _ := media["storageKey"].(string)
		if key == "" {
			return nil
		}
		asset, err := s.repo.AssetForUser(task.UserID, recoveredMediaAssetID(task.ID, index))
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if asset == nil {
			if !representationsLoaded {
				representations, err = s.repo.AssetRepresentationsForTask(task.ID)
				if err != nil {
					return err
				}
				representationsLoaded = true
			}
			for _, representation := range representations {
				if "resource:"+representation.ResourceID != key || representation.AssetVersionID == "" {
					continue
				}
				version, versionErr := s.repo.AssetVersion(representation.AssetVersionID)
				if versionErr != nil {
					return versionErr
				}
				asset, err = s.repo.AssetForUser(task.UserID, version.AssetID)
				if err != nil {
					return err
				}
				break
			}
		}
		if asset == nil {
			return nil
		}
		var payload struct {
			Data struct {
				StorageKey string `json:"storageKey"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(asset.PayloadJSON), &payload); err != nil {
			return err
		}
		if payload.Data.StorageKey == key {
			media["assetId"] = asset.ID
		}
		return nil
	}
	for _, name := range []string{"images", "audios"} {
		if items, ok := result[name].([]any); ok {
			for index, item := range items {
				if media, ok := item.(map[string]any); ok {
					if err := bind(media, index); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, name := range []string{"video", "audio"} {
		if media, ok := result[name].(map[string]any); ok {
			if err := bind(media, 0); err != nil {
				return err
			}
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	task.ResultJSON = string(encoded)
	return nil
}
