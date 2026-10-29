package repository

import (
	"encoding/json"

	"yingce/backend/internal/assets"
	"yingce/backend/internal/model"
)

// MediaVersionScope 是节点化身守卫（画布纪元 + 节点实体 generation）。
type MediaVersionScope struct {
	OwnerID, CanvasID, Epoch, NodeID string
	Generation                       int64
}

type canvasVersionDocument struct {
	Nodes []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Metadata struct {
			Versions []struct {
				ID    string `json:"id"`
				Media struct {
					StorageKey string `json:"storageKey"`
					Content    string `json:"content"`
				} `json:"media"`
			} `json:"mediaVersions"`
		} `json:"metadata"`
	} `json:"nodes"`
}

// MediaVersionResourceReferences 扫描当前画布节点的 metadata.mediaVersions：历史版本仍持有资源，
// 受保护删除前要列为直接引用（来源写明画布与节点）。版本历史只存在节点 metadata 中。
func (r *Repository) MediaVersionResourceReferences(owner string, ids []string) ([]ResourceDirectReference, error) {
	refs := []ResourceDirectReference{}
	if len(ids) == 0 {
		return refs, nil
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var canvases []model.CanvasProject
	if err := r.db.Select("id", "title", "payload_json").Where("user_id = ? AND payload_json LIKE ?", owner, "%mediaVersions%").Find(&canvases).Error; err != nil {
		return nil, err
	}
	for _, canvas := range canvases {
		var doc canvasVersionDocument
		if err := json.Unmarshal([]byte(canvas.PayloadJSON), &doc); err != nil {
			return nil, err
		}
		for _, node := range doc.Nodes {
			seen := map[string]bool{}
			for _, version := range node.Metadata.Versions {
				for _, locator := range []string{version.Media.StorageKey, version.Media.Content} {
					id := assets.ResourceID(locator)
					if id != "" && wanted[id] && !seen[id] {
						seen[id] = true
						title := canvas.Title
						if node.Title != "" {
							title += " / " + node.Title
						}
						refs = append(refs, ResourceDirectReference{Kind: "画布媒体版本", ID: canvas.ID + ":" + node.ID, Title: title, ResourceID: id})
					}
				}
			}
		}
	}
	return refs, nil
}

// UpdateCanvasTaskBindingMediaVersion 写入媒体版本任务的冻结上下文或写回结果。
func (r *Repository) UpdateCanvasTaskBindingMediaVersion(taskID, owner, value string) error {
	return r.db.Model(&model.CanvasTaskBinding{}).Where("task_id = ? AND owner_id = ?", taskID, owner).Update("media_version_json", value).Error
}
