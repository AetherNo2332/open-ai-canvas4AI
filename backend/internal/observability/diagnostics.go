package observability

const DiagnosticMaxClientEvents = 500

type DiagnosticEstimate struct {
	ClientEventLimit int
	TaskCount        int
	TaskLogCount     int
	APICallCount     int
	EstimatedBytes   int64
	WillTruncate     bool
}

func EstimateDiagnosticPreview(clientEvents int, tasks int, taskLogs int, apiCalls int, truncated bool) DiagnosticEstimate {
	return DiagnosticEstimate{
		ClientEventLimit: DiagnosticMaxClientEvents,
		TaskCount:        tasks,
		TaskLogCount:     taskLogs,
		APICallCount:     apiCalls,
		EstimatedBytes:   int64(2048 + clientEvents*420 + tasks*620 + taskLogs*700 + apiCalls*520),
		WillTruncate:     truncated,
	}
}

func TailWithinLimit[T any](items []T, limit int) ([]T, bool) {
	if limit < 0 {
		limit = 0
	}
	if len(items) <= limit {
		return items, false
	}
	return items[len(items)-limit:], true
}
