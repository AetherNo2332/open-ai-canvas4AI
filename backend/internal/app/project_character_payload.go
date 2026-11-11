package app

import (
	"encoding/json"
	"time"
	"yingce/backend/internal/model"
)

func normalizedCharacterDefinition(value map[string]any) (json.RawMessage, error) {
	if value == nil {
		value = map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, BadAuthRequest("角色设定格式无效")
	}
	return encoded, nil
}

func characterAssetPayload(assetID string, versionID string, name string, definition json.RawMessage, createdAt time.Time, updatedAt time.Time) (string, error) {
	var value map[string]any
	if err := json.Unmarshal(definition, &value); err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"id": assetID, "kind": "entity", "category": model.AssetCategoryCharacter, "status": model.AssetVersionStatusConfirmed,
		"primaryVersionId": versionID, "title": name, "coverUrl": "", "tags": []string{}, "data": map[string]any{"definition": value},
		"createdAt": createdAt.Format(time.RFC3339Nano), "updatedAt": updatedAt.Format(time.RFC3339Nano),
	})
	return string(payload), err
}

func voiceProfileSummary(profile model.VoiceProfile) VoiceProfileSummary {
	compatible := []string{}
	_ = json.Unmarshal([]byte(profile.CompatibleModelsJSON), &compatible)
	return VoiceProfileSummary{ID: profile.ID, Name: profile.Name, Provider: profile.Provider, VoiceKey: profile.VoiceKey, Language: profile.Language, Timbre: profile.Timbre, SampleResourceID: profile.SampleResourceID, CompatibleModels: compatible, Status: profile.Status}
}

func validCharacterRepresentationRole(role string) bool {
	switch role {
	case "primary", "front", "side", "back", "turnaround_sheet", "expression_sheet":
		return true
	default:
		return false
	}
}
