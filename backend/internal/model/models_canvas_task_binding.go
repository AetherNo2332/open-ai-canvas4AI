package model

import "time"

// A task's source epoch and target are immutable. Terminal processing never
// recreates a deleted node or writes into a restored document epoch.
type CanvasTaskBinding struct {
	TaskID            string `gorm:"primaryKey;size:36"`
	OwnerID           string `gorm:"index;size:36;uniqueIndex:idx_canvas_task_operation,priority:1"`
	CanvasID          string `gorm:"index;size:80;uniqueIndex:idx_canvas_task_operation,priority:2"`
	Epoch             string `gorm:"size:80;uniqueIndex:idx_canvas_task_operation,priority:3"`
	ActorID           string `gorm:"size:80;uniqueIndex:idx_canvas_task_operation,priority:4"`
	ClientID          string `gorm:"size:128;uniqueIndex:idx_canvas_task_operation,priority:5"`
	OperationID       string `gorm:"size:128;uniqueIndex:idx_canvas_task_operation,priority:6"`
	TargetNodeID      string `gorm:"size:80"`
	Mode              string `gorm:"size:24"`
	ConsumerJSON      string `gorm:"type:text"`
	ResultContextJSON string `gorm:"type:text"`
	OriginalContent   string `gorm:"type:text"`
	// MediaVersionJSON 冻结媒体版本任务提交时的上下文（mediaversions.Base）；空表示不是媒体版本任务。
	MediaVersionJSON string `gorm:"type:text"`
	ResolutionVersion int
	CreatedAt         time.Time
}
type CanvasMaterializationJob struct {
	TaskID        string `gorm:"primaryKey;size:36"`
	OwnerID       string `gorm:"index;size:36"`
	State         string `gorm:"index;size:24"`
	Outcome       string `gorm:"size:80"`
	Attempts      int
	LastError     string     `gorm:"size:240"`
	NextAttemptAt *time.Time `gorm:"index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
type CanvasTaskOutput struct {
	TaskID      string `gorm:"primaryKey;size:36"`
	OutputIndex int    `gorm:"primaryKey"`
	OwnerID     string `gorm:"index;size:36"`
	AssetID     string `gorm:"size:80"`
	ResourceID  string `gorm:"size:80"`
	Kind        string `gorm:"size:24"`
	PayloadJSON string `gorm:"type:text"`
	CreatedAt   time.Time
}

// Frozen input resource guards survive source-node deletion and process restart.
type CanvasTaskResource struct {
	TaskID     string `gorm:"primaryKey;size:36"`
	ResourceID string `gorm:"primaryKey;index;size:80"`
	OwnerID    string `gorm:"index;size:36"`
}
