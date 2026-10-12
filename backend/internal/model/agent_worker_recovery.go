package model

import "time"

const AgentWorkerRecoveryLimit = 5
const AgentWorkerRecoveryWindow = 10 * time.Minute

func (run *CloudAgentExecution) WorkerRecoveryExhausted(now time.Time) bool {
	return run.RecoveryAttempts >= AgentWorkerRecoveryLimit ||
		run.RecoveryOperationAttempts >= AgentWorkerRecoveryLimit ||
		(run.RecoveryStartedAt != nil && !now.Before(run.RecoveryStartedAt.Add(AgentWorkerRecoveryWindow)))
}
