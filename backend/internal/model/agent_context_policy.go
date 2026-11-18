package model

type AgentSharedPreferences struct {
	Language  string `json:"language,omitempty"`
	Tone      string `json:"tone,omitempty"`
	Verbosity string `json:"verbosity,omitempty"`
	Format    string `json:"format,omitempty"`
	Revision  int64  `json:"revision"`
}

type UserAgentSharedPreferences struct {
	UserID                 string `gorm:"primaryKey;size:80"`
	AgentSharedPreferences `gorm:"embedded"`
}

type AgentScopeMemoryKey struct {
	UserID    string `gorm:"primaryKey;size:80"`
	ScopeType string `gorm:"primaryKey;size:40"`
	ScopeID   string `gorm:"primaryKey;size:80"`
}

type AgentScopeMemory struct {
	AgentScopeMemoryKey `gorm:"embedded"`
	EntriesJSON         string
	Revision            int64
	NextEntrySeq        int64
}

type AgentScopeMemoryWrite struct {
	AgentScopeMemoryKey `gorm:"embedded"`
	RunID               string `gorm:"primaryKey;size:80"`
	CallID              string `gorm:"primaryKey;size:160"`
	OutcomeJSON         string
}
