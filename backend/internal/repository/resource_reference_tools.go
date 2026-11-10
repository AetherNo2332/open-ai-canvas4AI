package repository

import (
	"encoding/json"
	"slices"
	"strconv"
	"yingce/backend/internal/model"
)

func (r *Repository) appendToolResourceReferences(snapshot *ResourceReferenceSnapshot, userID string, resourceIDs []string) error {
	var toolRecords []model.Tool
	if err := r.db.Where("owner_id = ?", userID).Find(&toolRecords).Error; err != nil {
		return err
	}
	for _, tool := range toolRecords {
		var extra []string
		if tool.ExtraInfoJSON != "" {
			if err := json.Unmarshal([]byte(tool.ExtraInfoJSON), &extra); err != nil {
				return err
			}
		}
		payload, err := json.Marshal(map[string]any{"coverUrl": tool.Cover, "url": tool.MediaURL, "referenceUrls": extra})
		if err != nil {
			return err
		}
		snapshot.Documents = append(snapshot.Documents, ResourceReferenceDocument{Kind: "工具", ID: strconv.FormatInt(tool.ID, 10), Title: tool.Label, PrimaryJSON: string(payload)})
	}
	var workbenchTools []model.WorkbenchTool
	if err := r.db.Where("(icon_resource_id IN ? OR cover_resource_id IN ?) AND enabled = ?", resourceIDs, resourceIDs, true).Find(&workbenchTools).Error; err != nil {
		return err
	}
	for _, tool := range workbenchTools {
		if tool.IconResourceID != "" && slices.Contains(resourceIDs, tool.IconResourceID) {
			snapshot.Direct = append(snapshot.Direct, ResourceDirectReference{Kind: "工作台工具图标", ID: tool.ID, Title: tool.Name, ResourceID: tool.IconResourceID})
		}
		if tool.CoverResourceID != "" && slices.Contains(resourceIDs, tool.CoverResourceID) {
			snapshot.Direct = append(snapshot.Direct, ResourceDirectReference{Kind: "工作台工具封面", ID: tool.ID, Title: tool.Name, ResourceID: tool.CoverResourceID})
		}
	}

	return nil
}
