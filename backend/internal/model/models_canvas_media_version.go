package model

import "time"

// CanvasMediaVersion is scoped to a document/node incarnation. Output bytes
// remain in Resource; changing the current version never mutates this lineage.
type CanvasMediaVersion struct {
	ID                     string    `json:"id" gorm:"primaryKey;size:80"`
	OwnerID                string    `json:"-" gorm:"size:36;index;uniqueIndex:idx_canvas_media_sequence,priority:1"`
	CanvasID               string    `json:"canvasId" gorm:"size:80;uniqueIndex:idx_canvas_media_sequence,priority:2"`
	Epoch                  string    `json:"-" gorm:"size:80;uniqueIndex:idx_canvas_media_sequence,priority:3"`
	NodeID                 string    `json:"nodeId" gorm:"size:80;uniqueIndex:idx_canvas_media_sequence,priority:4"`
	Generation             int64     `json:"-" gorm:"uniqueIndex:idx_canvas_media_sequence,priority:5"`
	Sequence               int64     `json:"sequence" gorm:"uniqueIndex:idx_canvas_media_sequence,priority:6"`
	TaskID                 *string   `json:"taskId,omitempty" gorm:"size:36;uniqueIndex"`
	SourceTaskID           string    `json:"sourceTaskId,omitempty" gorm:"size:36"`
	SourceNodeID           string    `json:"sourceNodeId,omitempty" gorm:"size:80"`
	SourceVersionID        string    `json:"sourceVersionId,omitempty" gorm:"size:80"`
	ParentVersionID        string    `json:"parentVersionId,omitempty" gorm:"size:80"`
	Mode                   string    `json:"mode" gorm:"size:16"`
	Status                 string    `json:"status" gorm:"size:32;index"`
	SelectionRevision      int64     `json:"-"`
	RequestJSON            string    `json:"-" gorm:"type:text"`
	MediaJSON              string    `json:"-" gorm:"type:text"`
	OutputsJSON            string    `json:"-" gorm:"type:text"`
	ResourceID             string    `json:"resourceId,omitempty" gorm:"size:80;index"`
	Adoption               string    `json:"adoption" gorm:"size:48"`
	AutoAdoptBlockedReason string    `json:"-" gorm:"size:48"`
	Error                  string    `json:"error,omitempty" gorm:"size:500"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}
