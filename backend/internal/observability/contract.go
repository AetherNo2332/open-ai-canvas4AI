package observability

import "time"

type EventKind string

const (
	KindTask       EventKind = "task.lifecycle"
	KindExecute    EventKind = "agent.execute"
	KindQueue      EventKind = "queue.wait"
	KindWorker     EventKind = "worker.claim"
	KindLLM        EventKind = "llm.call"
	KindTool       EventKind = "tool.call"
	KindCompaction EventKind = "context.compaction"
)

type Status string

const (
	StatusQueued       Status = "queued"
	StatusClaimed      Status = "claimed"
	StatusRunning      Status = "running"
	StatusWaitingModel Status = "waiting_model"
	StatusWaitingTool  Status = "waiting_tool"
	StatusCompacting   Status = "compacting"
	StatusCompleted    Status = "completed"
	StatusFailed       Status = "failed"
	StatusCancelled    Status = "cancelled"
	StatusExpired      Status = "expired"
)

type Event struct {
	Key              string
	ParentRunID      string
	TaskID           string
	RunID            string
	TraceID          string
	SessionID        string
	AgentVersion     string
	PromptVersion    string
	Model            string
	Pool             string
	Role             string
	ToolType         string
	Kind             EventKind
	Status           Status
	StartedAt        time.Time
	EndedAt          time.Time
	InputTokens      int64
	OutputTokens     int64
	CachedTokens     int64
	ReasoningTokens  int64
	CostMicrocredits *int64
	RetryOf          string
	Reason           string
}

type Snapshot struct {
	Available      bool              `json:"available"`
	Delayed        bool              `json:"delayed"`
	GeneratedAt    time.Time         `json:"generatedAt"`
	Window         time.Duration     `json:"window"`
	GrafanaURL     string            `json:"grafanaUrl,omitempty"`
	Worker         WorkerSnapshot    `json:"worker"`
	Queue          QueueSnapshot     `json:"queue"`
	Tasks          TaskSnapshot      `json:"tasks"`
	Tools          ToolSnapshot      `json:"tools"`
	LLM            LLMSnapshot       `json:"llm"`
	Cost           CostSnapshot      `json:"cost"`
	Quality        QualitySnapshot   `json:"quality"`
	Alerts         []string          `json:"alerts"`
	RecentFailures []FailureSnapshot `json:"recentFailures"`
}

type WorkerSnapshot struct {
	Online    int     `json:"online"`
	Busy      int     `json:"busy"`
	BusyRatio float64 `json:"busyRatio"`
}
type QueueSnapshot struct {
	Depth            int   `json:"depth"`
	OldestAgeSeconds int64 `json:"oldestAgeSeconds"`
}
type TaskSnapshot struct {
	Total        int     `json:"total"`
	Completed    int     `json:"completed"`
	Failed       int     `json:"failed"`
	Retried      int     `json:"retried"`
	SuccessRate  float64 `json:"successRate"`
	P50LatencyMs int64   `json:"p50LatencyMs"`
	P95LatencyMs int64   `json:"p95LatencyMs"`
	P99LatencyMs int64   `json:"p99LatencyMs"`
}
type ToolSnapshot struct {
	Calls        int     `json:"calls"`
	Succeeded    int     `json:"succeeded"`
	SuccessRate  float64 `json:"successRate"`
	StepsPerTask float64 `json:"stepsPerTask"`
}
type LLMSnapshot struct {
	Calls           int   `json:"calls"`
	InputTokens     int64 `json:"inputTokens"`
	OutputTokens    int64 `json:"outputTokens"`
	CachedTokens    int64 `json:"cachedTokens"`
	ReasoningTokens int64 `json:"reasoningTokens"`
}
type CostSnapshot struct {
	Microcredits      *int64 `json:"microcredits"`
	PerSuccessfulTask *int64 `json:"perSuccessfulTask"`
	Known             bool   `json:"known"`
}
type QualitySnapshot struct {
	Score         *float64 `json:"score"`
	FeedbackCount int      `json:"feedbackCount"`
}
type FailureSnapshot struct {
	TaskID  string    `json:"taskId"`
	RunID   string    `json:"runId"`
	TraceID string    `json:"traceId"`
	Status  Status    `json:"status"`
	Reason  string    `json:"reason"`
	At      time.Time `json:"at"`
}
