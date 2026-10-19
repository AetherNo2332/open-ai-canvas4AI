package repository

type AssetCanvasOrigin struct {
	TaskID     string
	CanvasID   string
	CanvasName string
}

// Restrict both sides of task/canvas lookups to the current owner; select only
// names and IDs so an asset page never loads prompts or full canvas documents.
func (r *Repository) AssetCanvasOrigins(userID string, canvasIDs, taskIDs []string) (map[string]AssetCanvasOrigin, error) {
	result := make(map[string]AssetCanvasOrigin)
	var rows []AssetCanvasOrigin
	if len(canvasIDs) > 0 {
		if err := r.db.Table("canvas_projects").Select("id AS canvas_id,title AS canvas_name").Where("user_id = ? AND id IN ?", userID, canvasIDs).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			result["canvas:"+row.CanvasID] = row
		}
	}
	if len(taskIDs) > 0 {
		rows = nil
		if err := r.db.Table("tasks AS t").Select("t.id AS task_id,c.id AS canvas_id,c.title AS canvas_name").Joins("JOIN canvas_projects AS c ON c.id = t.project_id AND c.user_id = t.user_id").Where("t.user_id = ? AND t.id IN ?", userID, taskIDs).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			result["task:"+row.TaskID] = row
		}
	}
	return result, nil
}
