package app

import (
	"encoding/json"
	"strings"
)

func projectAssetPreview(payloadJSON string) (string, string, string, int64) {
	var payload struct {
		Data struct {
			StorageKey string `json:"storageKey"`
			Content    string `json:"content"`
			Source     string `json:"source"`
			DurationMs int64  `json:"durationMs"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(payloadJSON), &payload) != nil {
		return "", "", "", 0
	}
	previewRunes := []rune(strings.TrimSpace(payload.Data.Content))
	if len(previewRunes) > 240 {
		previewRunes = append(previewRunes[:240], '…')
	}
	return strings.TrimSpace(payload.Data.StorageKey), string(previewRunes), strings.TrimSpace(payload.Data.Source), payload.Data.DurationMs

}
