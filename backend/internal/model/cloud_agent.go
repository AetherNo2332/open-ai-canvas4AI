package model

import "time"

// CloudAgentExecution checkpoints orchestration independently of billed tasks.
type CloudAgentExecution struct {
	ID       string `gorm:"primaryKey;size:80"`
	UserID   string `gorm:"index;size:36"`
	Status   string `gorm:"index;size:32"`
	Revision int64
	// Control fields remain writable even when the transcript cannot be decoded or saved.
	CanvasID       string `gorm:"size:80"`
	ActiveTaskID   string `gorm:"size:80"`
	MediaTaskID    string `gorm:"size:80"`
	CleanupPending bool   `gorm:"not null;default:false;index"`
	FailureMessage string `gorm:"size:1000"`
	StateJSON      string `gorm:"type:text"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CloudAgentCanvasMutation records one atomic canvas change made by an Agent.
// BeforeJSON is intentionally bounded by the application layer; mutations that
// cannot retain a safe snapshot are marked not_undoable instead of truncating it.
type CloudAgentCanvasMutation struct {
	ID                 string     `json:"id" gorm:"primaryKey;size:80"`
	RunID              string     `json:"runId" gorm:"index;size:80"`
	UserID             string     `json:"userId" gorm:"index;size:36"`
	CanvasID           string     `json:"canvasId" gorm:"index;size:80"`
	StepID             string     `json:"stepId" gorm:"index;size:160"`
	Operation          string     `json:"operation" gorm:"size:64"`
	BeforeSnapshotHash string     `json:"beforeSnapshotHash" gorm:"size:64"`
	AfterSnapshotHash  string     `json:"afterSnapshotHash" gorm:"size:64"`
	BeforeJSON         string     `json:"-" gorm:"type:text"`
	HasSubmittedTask   bool       `json:"hasSubmittedTask"`
	Status             string     `json:"status" gorm:"index;size:24"`
	CreatedAt          time.Time  `json:"createdAt" gorm:"index"`
	UndoneAt           *time.Time `json:"undoneAt,omitempty"`
}

// CloudAgentRunEvent 是运行事件日志的落库形态：append-only、按 (runID, seq) 唯一。
//
// 事件过去存在 CloudAgentExecution.StateJSON 的 events 数组里，导致"能跑多少轮"被
// 运行记录体积决定（写入型会话 22 步就撞 512KiB 上限）。搬到独立表后状态只保留尾部缓存，
// 运行详情按 seq 分页读取；ExpiresAt 供保留期清理使用。
type CloudAgentRunEvent struct {
	ID        uint64    `json:"-" gorm:"primaryKey;autoIncrement"`
	RunID     string    `json:"runId" gorm:"size:80;not null;uniqueIndex:idx_cloud_agent_run_events_seq,priority:1;index:idx_cloud_agent_run_events_canvas,priority:1"`
	UserID    string    `json:"-" gorm:"size:36;not null;index"`
	CanvasID  string    `json:"-" gorm:"size:80;index:idx_cloud_agent_run_events_canvas,priority:2"`
	Seq       int       `json:"seq" gorm:"not null;uniqueIndex:idx_cloud_agent_run_events_seq,priority:2"`
	EventID   string    `json:"eventId" gorm:"size:240;not null"`
	Type      string    `json:"type" gorm:"size:64;not null;index"`
	Payload   string    `json:"payload" gorm:"type:text;not null"`
	CreatedAt time.Time `json:"createdAt" gorm:"index"`
	ExpiresAt time.Time `json:"-" gorm:"index"`
}
