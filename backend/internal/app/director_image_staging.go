package app

import (
	"encoding/json"
	"errors"
	"time"
)

// 图片→导演 Agent 流程的服务端 staging 草稿（P4 流程接线 U1）。
//
// image_to_director 工作流的导演工具编译后直接写权威场景；连续性失败或
// 运行取消时会留下半成品场景，违反入口指令「失败或取消保持画布不变」。
// 本层把场景创建先落到独立 staging 草稿：检查通过（或用户确认）后才能
// 原子提交。语义与已验收的前端事务 web/src/lib/canvas/previs/
// previs-image-transaction.ts 保持同一矩阵。
type DirectorImageStagingStatus string

const (
	DirectorImageStagingStaging   DirectorImageStagingStatus = "staging"
	DirectorImageStagingApproved  DirectorImageStagingStatus = "approved"
	DirectorImageStagingCommitted DirectorImageStagingStatus = "committed"
	DirectorImageStagingRejected  DirectorImageStagingStatus = "rejected"
)

type DirectorImageStagingDraft struct {
	ID      string
	RunID   string
	SceneID string
	// 创建时权威画布的快照哈希——提交时的 CAS 基线。
	BaseCanvasHash string
	Scene          json.RawMessage
	Status         DirectorImageStagingStatus
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

type DirectorImageStagingCreateOptions struct {
	ID             string
	RunID          string
	SceneID        string
	BaseCanvasHash string
	Scene          json.RawMessage
	Now            time.Time
	TTL            time.Duration
}

var errDirectorStagingInvalid = errors.New("invalid director image staging input")

func isNonEmptyJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	trimmed := raw
	if len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\n' || trimmed[0] == '\r') {
		for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\n' || trimmed[0] == '\r') {
			trimmed = trimmed[1:]
		}
	}
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// CreateDirectorImageStagingDraft 从权威场景的 JSON 生成独立 staging 草稿。
func CreateDirectorImageStagingDraft(options DirectorImageStagingCreateOptions) (*DirectorImageStagingDraft, error) {
	if options.ID == "" || options.RunID == "" || options.SceneID == "" || options.BaseCanvasHash == "" {
		return nil, errDirectorStagingInvalid
	}
	if !isNonEmptyJSONObject(options.Scene) {
		return nil, errDirectorStagingInvalid
	}
	if options.TTL <= 0 || options.Now.IsZero() {
		return nil, errDirectorStagingInvalid
	}
	scene := make(json.RawMessage, len(options.Scene))
	copy(scene, options.Scene)
	return &DirectorImageStagingDraft{
		ID:             options.ID,
		RunID:          options.RunID,
		SceneID:        options.SceneID,
		BaseCanvasHash: options.BaseCanvasHash,
		Scene:          scene,
		Status:         DirectorImageStagingStaging,
		CreatedAt:      options.Now,
		ExpiresAt:      options.Now.Add(options.TTL),
	}, nil
}

// ApproveDirectorImageStagingDraft staging → approved（连续性检查通过或用户确认警告后调用）。
// 非 staging 状态为无操作。
func ApproveDirectorImageStagingDraft(draft *DirectorImageStagingDraft) {
	if draft == nil || draft.Status != DirectorImageStagingStaging {
		return
	}
	draft.Status = DirectorImageStagingApproved
}

// RejectDirectorImageStagingDraft 取消草稿：进入终态 rejected，不再可提交。
// 已提交/已拒绝为无操作。
func RejectDirectorImageStagingDraft(draft *DirectorImageStagingDraft) {
	if draft == nil {
		return
	}
	if draft.Status == DirectorImageStagingCommitted || draft.Status == DirectorImageStagingRejected {
		return
	}
	draft.Status = DirectorImageStagingRejected
}

type DirectorImageStagingCommitResult struct {
	OK     bool
	Reason string
	Scene  json.RawMessage
}

// CommitDirectorImageStagingDraft 原子提交：草稿必须已批准、CAS 基线未漂移
// 且在有效期内。状态机把草稿推入终态——同一草稿无论重复调用还是重试，
// 第二次必然得到 already-committed，不可能产生第二次场景提交。
func CommitDirectorImageStagingDraft(draft *DirectorImageStagingDraft, authoritativeHash string, now time.Time) DirectorImageStagingCommitResult {
	if draft == nil {
		return DirectorImageStagingCommitResult{OK: false, Reason: "rejected"}
	}
	switch draft.Status {
	case DirectorImageStagingCommitted:
		return DirectorImageStagingCommitResult{OK: false, Reason: "already-committed"}
	case DirectorImageStagingRejected:
		return DirectorImageStagingCommitResult{OK: false, Reason: "rejected"}
	}
	if draft.BaseCanvasHash != authoritativeHash {
		return DirectorImageStagingCommitResult{OK: false, Reason: "scene-drift"}
	}
	if draft.Status != DirectorImageStagingApproved {
		return DirectorImageStagingCommitResult{OK: false, Reason: "not-approved"}
	}
	if now.After(draft.ExpiresAt) {
		draft.Status = DirectorImageStagingRejected
		return DirectorImageStagingCommitResult{OK: false, Reason: "expired"}
	}
	draft.Status = DirectorImageStagingCommitted
	scene := make(json.RawMessage, len(draft.Scene))
	copy(scene, draft.Scene)
	return DirectorImageStagingCommitResult{OK: true, Scene: scene}
}

// ExpireDirectorImageStagingDrafts 过期清理：staging/approved 超期草稿标记
// rejected；按 ID 去重（首次出现为准）。
func ExpireDirectorImageStagingDrafts(drafts []*DirectorImageStagingDraft, now time.Time) []*DirectorImageStagingDraft {
	out := make([]*DirectorImageStagingDraft, 0, len(drafts))
	seen := make(map[string]bool, len(drafts))
	for _, draft := range drafts {
		if draft == nil || seen[draft.ID] {
			continue
		}
		seen[draft.ID] = true
		if (draft.Status == DirectorImageStagingStaging || draft.Status == DirectorImageStagingApproved) && now.After(draft.ExpiresAt) {
			draft.Status = DirectorImageStagingRejected
		}
		out = append(out, draft)
	}
	return out
}

// NewDirectorImageStagingReceipt 为 staging 相关工具结果附稳定回执信封，
// 与 cloudAgentDirectorReceipt 的 requestId/callId 关联点保持一致。
func NewDirectorImageStagingReceipt(runID, callID string, draft *DirectorImageStagingDraft) map[string]any {
	if draft == nil {
		return map[string]any{"requestId": runID, "callId": callID, "stagingId": "", "status": string(DirectorImageStagingRejected)}
	}
	return map[string]any{
		"requestId": runID,
		"callId":    callID,
		"stagingId": draft.ID,
		"sceneId":   draft.SceneID,
		"status":    string(draft.Status),
		"expiresAt": draft.ExpiresAt.UTC().Format(time.RFC3339),
	}
}
