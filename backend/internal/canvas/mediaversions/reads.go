package mediaversions

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"time"

	"infinite-canvas/backend/internal/repository"
)

type Item struct {
	VersionID  string         `json:"versionId"`
	Sequence   int64          `json:"sequence"`
	Mode       string         `json:"mode"`
	Status     string         `json:"status"`
	TaskID     *string        `json:"taskId,omitempty"`
	ResourceID string         `json:"resourceId,omitempty"`
	Media      map[string]any `json:"media,omitempty"`
	Prompt     string         `json:"prompt,omitempty"`
	Model      string         `json:"model,omitempty"`
	Options    map[string]any `json:"options,omitempty"`
	Outputs    []Output       `json:"outputs,omitempty"`
	Adoption   string         `json:"adoption"`
	Error      string         `json:"error,omitempty"`
	IsCurrent  bool           `json:"isCurrent"`
	IsPending  bool           `json:"isPending"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type Output struct {
	Index      int            `json:"index"`
	Mode       string         `json:"mode"`
	ResourceID string         `json:"resourceId"`
	AssetID    string         `json:"assetId"`
	Media      map[string]any `json:"media"`
}

type Page struct {
	State             State  `json:"state"`
	CanCreateVersion  bool   `json:"canCreateVersion"`
	BlockReason       string `json:"blockReason,omitempty"`
	BlockIssue        string `json:"blockIssue,omitempty"`
	Items             []Item `json:"items"`
	NextBeforeVersion int64  `json:"nextBeforeVersion,omitempty"`
}

// Read returns a fixed-size, credential-free projection. Requests and provider
// diagnostics stay in the task service instead of leaking through version UI.
func Read(repo *repository.Repository, scope Scope, node map[string]any, before int64) (Page, error) {
	out := Page{State: ReadState(node), Items: []Item{}}
	if before < 0 {
		return out, fail("invalid_cursor", "版本游标无效")
	}
	rows, err := repo.CanvasMediaVersions(scope, before, 21)
	if err != nil {
		return out, err
	}
	if len(rows) > 20 {
		out.NextBeforeVersion = rows[19].Sequence
		rows = rows[:20]
	}
	for _, row := range rows {
		var request struct {
			Prompt string `json:"prompt"`
			Model  string `json:"model"`
			Input  struct {
				Config map[string]any `json:"config"`
			} `json:"input"`
		}
		if row.RequestJSON != "" {
			if err := json.Unmarshal([]byte(row.RequestJSON), &request); err != nil {
				return out, err
			}
		}
		options := map[string]any{}
		for _, key := range []string{"size", "quality", "transparentBackground", "count", "videoSeconds", "vquality", "videoGenerateAudio", "audioVoice", "audioFormat", "audioSpeed", "audioInstructions"} {
			if value, ok := request.Input.Config[key]; ok {
				options[key] = value
			}
		}
		var media map[string]any
		if row.Status == "ready" {
			if err := json.Unmarshal([]byte(row.MediaJSON), &media); err != nil {
				return out, err
			}
		}
		var records []model.CanvasTaskOutput
		if row.OutputsJSON != "" {
			if err := json.Unmarshal([]byte(row.OutputsJSON), &records); err != nil {
				return out, err
			}
		}
		outputs := []Output{}
		for _, record := range records {
			var payload struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal([]byte(record.PayloadJSON), &payload); err != nil {
				return out, err
			}
			data := payload.Data
			if data == nil {
				data = map[string]any{}
			}
			data["content"] = data["url"]
			outputs = append(outputs, Output{Index: record.OutputIndex, Mode: record.Kind, ResourceID: record.ResourceID, AssetID: record.AssetID, Media: data})
		}
		out.Items = append(out.Items, Item{VersionID: row.ID, Sequence: row.Sequence, Mode: row.Mode, Status: row.Status, TaskID: row.TaskID, ResourceID: row.ResourceID, Media: media, Prompt: request.Prompt, Model: request.Model, Options: options, Outputs: outputs, Adoption: row.Adoption, Error: row.Error, IsCurrent: row.ID == out.State.CurrentVersionID, IsPending: row.ID == out.State.PendingVersionID, CreatedAt: row.CreatedAt})
	}
	if err = CheckAdmission(repo, scope, node, "new_version"); err == nil {
		out.CanCreateVersion = true
	} else if e, ok := err.(*Error); ok {
		out.BlockReason = e.Reason
		out.BlockIssue = e.Message
	} else {
		return out, err
	}
	return out, nil
}
