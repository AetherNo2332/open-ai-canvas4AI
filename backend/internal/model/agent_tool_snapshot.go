package model

import "time"

type AgentToolSnapshotCounter struct {
	ID    int `gorm:"primaryKey;autoIncrement:false"`
	Value int64
}
type AgentToolSnapshotContent struct {
	ID       string `gorm:"primaryKey;size:80"`
	Contents string `gorm:"type:text"`
}

// Contents are immutable. Registrations reference the loaded process artifact.
type AgentToolSnapshot struct {
	ID            string    `gorm:"primaryKey;size:80" json:"id"`
	Source        string    `gorm:"size:20" json:"source"`
	InstanceID    string    `gorm:"size:80" json:"instanceId"`
	BuildCommit   string    `gorm:"size:120" json:"buildCommit"`
	SchemaVersion string    `gorm:"size:120" json:"schemaVersion"`
	ToolCount     int       `json:"toolCount"`
	Contents      string    `gorm:"-" json:"-"`
	ContentID     string    `gorm:"size:80;index" json:"-"`
	ToolNames     []string  `gorm:"serializer:json;type:text" json:"toolNames"`
	CreatedAt     time.Time `json:"createdAt"`
}

type AgentToolRuntime struct {
	InstanceID        string    `gorm:"primaryKey;size:80" json:"instanceId"`
	BootID            string    `gorm:"size:80" json:"bootId"`
	SnapshotID        string    `gorm:"size:80;index" json:"snapshotId"`
	LoadGeneration    int64     `json:"loadGeneration"`
	LoadError         string    `gorm:"size:1000" json:"-"`
	ManualDraining    bool      `json:"manualDraining"`
	Revision          int64     `json:"revision"`
	LastHeartbeatAt   time.Time `gorm:"index" json:"lastHeartbeatAt"`
	ArtifactFileState string    `gorm:"size:20" json:"artifactFileState,omitempty"`
}

type AgentToolSnapshotAlert struct {
	ID                string `gorm:"primaryKey;size:80"`
	BackendSnapshotID string `gorm:"size:80;index"`
	WorkerSnapshotID  string `gorm:"size:80"`
	InstanceID        string `gorm:"size:80;index"`
	Code              string `gorm:"size:80"`
	FirstSeenAt       time.Time
	LastSeenAt        time.Time
	ResolvedAt        *time.Time
}
