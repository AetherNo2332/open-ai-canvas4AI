package agentservice

import (
	"strings"
	"time"
)

// Lease is the single-writer record of a Session. Epoch increases on every
// acquisition, so a writer holding an older epoch can never write again.
type Lease struct {
	Owner     string
	RunID     string
	Epoch     int64
	ExpiresAt time.Time
}

func (l Lease) live(now time.Time) bool {
	return l.Owner != "" && now.Before(l.ExpiresAt)
}

// Acquire returns the next lease or session_conflict while another writer is live.
func (l Lease) Acquire(owner, runID string, now time.Time, ttl time.Duration) (Lease, error) {
	owner, runID = strings.TrimSpace(owner), strings.TrimSpace(runID)
	if owner == "" || runID == "" || ttl <= 0 {
		return Lease{}, NewError(CodeInvalidRequest, "写入者与运行不能为空")
	}
	if l.live(now) {
		return Lease{}, NewError(CodeSessionConflict, "会话正在处理上一条请求，请稍后再试")
	}
	return Lease{Owner: owner, RunID: runID, Epoch: l.Epoch + 1, ExpiresAt: now.Add(ttl)}, nil
}

// Check rejects writes from an expired lease or an older epoch.
func (l Lease) Check(owner string, epoch int64, now time.Time) error {
	if !l.live(now) || l.Owner != owner || l.Epoch != epoch {
		return NewError(CodeStaleLease, "会话写入者已更换，本次写入被拒绝")
	}
	return nil
}
