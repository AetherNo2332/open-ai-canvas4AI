package model

// PlatformTaskEvent 是平台任务的持久事件。状态与费用变化由数据库触发器在 tasks / billing_orders
// 更新的同一事务里写入；提交事件由仓储在提交事务里写入。
// Position 是对外游标：PostgreSQL 由 DEFERRABLE 约束触发器在提交时分配，SQLite 在写入时分配（写事务串行），
// 因此游标按提交顺序递增，回滚的事务不占用游标。ID 由数据库或仓储生成，不承担排序语义。
type PlatformTaskEvent struct {
	ID            string `json:"-" gorm:"primaryKey;size:36"`
	Position      *int64 `json:"position" gorm:"uniqueIndex"`
	TaskID        string `json:"taskId" gorm:"size:36;not null;index"`
	UserID        string `json:"-" gorm:"size:36;not null;index:idx_platform_events_scope,priority:1"`
	Application   string `json:"-" gorm:"size:32;not null;index:idx_platform_events_scope,priority:2"`
	ProjectRef    string `json:"-" gorm:"size:80;not null;default:''"`
	Type          string `json:"type" gorm:"size:32;not null"`
	TaskStatus    string `json:"-" gorm:"size:24;not null;default:''"`
	MediaStage    string `json:"-" gorm:"size:24;not null;default:''"`
	MediaPending  bool   `json:"-" gorm:"not null;default:false"`
	HasResources  bool   `json:"-" gorm:"not null;default:false"`
	BillingStatus string `json:"-" gorm:"size:24;not null;default:''"`
	// OccurredAtMs 是 Unix 毫秒：触发器在两种数据库里都能生成，清理比较不受时区与文本格式影响。
	OccurredAtMs int64 `json:"-" gorm:"not null;index"`
}

// PlatformEventCounter 是事件游标计数器（单行，id=1）与已清理水位；首个事件写入时由触发器建行。
type PlatformEventCounter struct {
	ID           int   `gorm:"primaryKey;autoIncrement:false"`
	Value        int64 `gorm:"not null;default:0"`
	PrunedBefore int64 `gorm:"not null;default:0"`
}
