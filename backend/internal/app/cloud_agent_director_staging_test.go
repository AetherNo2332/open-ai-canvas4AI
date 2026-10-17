package app

import (
	"encoding/json"
	"testing"
	"time"
)

// 图片→导演 Agent 流程的服务端 staging 草稿生命周期（P4 流程接线 U1）。
// 语义基准:web/src/lib/canvas/previs/previs-image-transaction.ts 已验收矩阵
// (staging → approved → 一次性原子提交;CAS 基线 + TTL;失败/取消回到 rejected)。
// 服务端存在意义:director_block_scene 直接写权威场景会在连续性失败/取消时
// 留下半成品场景;先 staging、检查通过(或用户确认)后原子提交。

func stagingTestScene(t *testing.T, count int) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"objects": count, "version": 1})
	if err != nil {
		t.Fatalf("marshal scene: %v", err)
	}
	return raw
}

func stagingTestDraft(t *testing.T, now time.Time, ttl time.Duration) *DirectorImageStagingDraft {
	return stagingTestDraftID(t, "stg-1", now, ttl)
}

func stagingTestDraftID(t *testing.T, id string, now time.Time, ttl time.Duration) *DirectorImageStagingDraft {
	t.Helper()
	draft, err := CreateDirectorImageStagingDraft(DirectorImageStagingCreateOptions{
		ID:             id,
		RunID:          "run-1",
		SceneID:        "scene-1",
		BaseCanvasHash: "hash-a",
		Scene:          stagingTestScene(t, 3),
		Now:            now,
		TTL:            ttl,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return draft
}

func TestDirectorImageStagingCreateValidatesInputs(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	valid := DirectorImageStagingCreateOptions{
		ID: "stg-1", RunID: "run-1", SceneID: "scene-1",
		BaseCanvasHash: "hash-a", Scene: stagingTestScene(t, 1),
		Now: now, TTL: time.Minute,
	}
	if _, err := CreateDirectorImageStagingDraft(valid); err != nil {
		t.Fatalf("valid create must succeed: %v", err)
	}
	draft := *stagingTestDraft(t, now, time.Minute)
	if draft.Status != DirectorImageStagingStaging || draft.BaseCanvasHash != "hash-a" {
		t.Fatalf("new draft must be staging on the given CAS baseline: %+v", draft)
	}
	if !draft.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("expiresAt = now + ttl, got %v", draft.ExpiresAt)
	}
	for name, mutate := range map[string]func(*DirectorImageStagingCreateOptions){
		"missing-id":          func(o *DirectorImageStagingCreateOptions) { o.ID = "" },
		"missing-run":         func(o *DirectorImageStagingCreateOptions) { o.RunID = "" },
		"missing-scene-id":    func(o *DirectorImageStagingCreateOptions) { o.SceneID = "" },
		"missing-cas-hash":    func(o *DirectorImageStagingCreateOptions) { o.BaseCanvasHash = "" },
		"empty-scene":         func(o *DirectorImageStagingCreateOptions) { o.Scene = nil },
		"invalid-scene-json":  func(o *DirectorImageStagingCreateOptions) { o.Scene = json.RawMessage(`17`) },
		"non-object-scene":    func(o *DirectorImageStagingCreateOptions) { o.Scene = json.RawMessage(`[1]`) },
		"zero-ttl":            func(o *DirectorImageStagingCreateOptions) { o.TTL = 0 },
		"negative-ttl":        func(o *DirectorImageStagingCreateOptions) { o.TTL = -time.Second },
		"zero-now":            func(o *DirectorImageStagingCreateOptions) { o.Now = time.Time{} },
	} {
		opts := valid
		mutate(&opts)
		if _, err := CreateDirectorImageStagingDraft(opts); err == nil {
			t.Errorf("%s: create must fail", name)
		}
	}
}

func TestDirectorImageStagingApproveRejectStateMachine(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)

	draft := stagingTestDraft(t, now, time.Minute)
	ApproveDirectorImageStagingDraft(draft)
	if draft.Status != DirectorImageStagingApproved {
		t.Fatalf("staging draft must approve to approved, got %v", draft.Status)
	}
	ApproveDirectorImageStagingDraft(draft) // 非 staging 时为无操作
	if draft.Status != DirectorImageStagingApproved {
		t.Fatalf("approve must no-op outside staging, got %v", draft.Status)
	}

	committed := stagingTestDraft(t, now, time.Minute)
	ApproveDirectorImageStagingDraft(committed)
	if res := CommitDirectorImageStagingDraft(committed, "hash-a", now); !res.OK {
		t.Fatalf("approved draft must commit, got %+v", res)
	}
	if committed.Status != DirectorImageStagingCommitted {
		t.Fatalf("commit must transition to committed, got %v", committed.Status)
	}
	RejectDirectorImageStagingDraft(committed)
	if committed.Status != DirectorImageStagingCommitted {
		t.Fatalf("reject must no-op on committed, got %v", committed.Status)
	}

	rejected := stagingTestDraft(t, now, time.Minute)
	RejectDirectorImageStagingDraft(rejected)
	if rejected.Status != DirectorImageStagingRejected {
		t.Fatalf("reject staging must yield rejected, got %v", rejected.Status)
	}
	ApproveDirectorImageStagingDraft(rejected)
	if rejected.Status != DirectorImageStagingRejected {
		t.Fatalf("approve must no-op on rejected, got %v", rejected.Status)
	}
}

func TestDirectorImageStagingCommitGates(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)

	cases := []struct {
		name        string
		setup       func(*DirectorImageStagingDraft)
		authHash    string
		at          time.Time
		wantOK      bool
		wantReason  string
		wantStatus  DirectorImageStagingStatus
	}{
		{
			name:       "unapproved draft cannot commit",
			setup:      func(d *DirectorImageStagingDraft) {},
			authHash:   "hash-a", at: now,
			wantOK: false, wantReason: "not-approved", wantStatus: DirectorImageStagingStaging,
		},
		{
			name:       "approved draft commits once",
			setup:      func(d *DirectorImageStagingDraft) { ApproveDirectorImageStagingDraft(d) },
			authHash:   "hash-a", at: now,
			wantOK: true, wantStatus: DirectorImageStagingCommitted,
		},
		{
			name:     "second commit is already-committed (幂等, 无二次提交)",
			setup:    func(d *DirectorImageStagingDraft) { ApproveDirectorImageStagingDraft(d) },
			authHash: "hash-a", at: now,
			// 注意:此用例在 commit 后再次 commit;断言在下方单独处理。
			wantOK: false,
		},
		{
			name:       "canvas drift blocks commit",
			setup:      func(d *DirectorImageStagingDraft) { ApproveDirectorImageStagingDraft(d) },
			authHash:   "hash-b", at: now,
			wantOK: false, wantReason: "scene-drift", wantStatus: DirectorImageStagingApproved,
		},
		{
			name:       "expired draft is rejected",
			setup:      func(d *DirectorImageStagingDraft) { ApproveDirectorImageStagingDraft(d) },
			authHash:   "hash-a", at: now.Add(61 * time.Second),
			wantOK: false, wantReason: "expired", wantStatus: DirectorImageStagingRejected,
		},
		{
			name:       "rejected draft cannot commit",
			setup:      func(d *DirectorImageStagingDraft) { RejectDirectorImageStagingDraft(d) },
			authHash:   "hash-a", at: now,
			wantOK: false, wantReason: "rejected", wantStatus: DirectorImageStagingRejected,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft := stagingTestDraft(t, now, time.Minute)
			tc.setup(draft)
			if tc.name == "second commit is already-committed (幂等, 无二次提交)" {
				first := CommitDirectorImageStagingDraft(draft, tc.authHash, tc.at)
				if !first.OK {
					t.Fatalf("first commit must succeed, got %+v", first)
				}
				second := CommitDirectorImageStagingDraft(draft, tc.authHash, tc.at)
				if second.OK || second.Reason != "already-committed" {
					t.Fatalf("second commit must be already-committed, got %+v", second)
				}
				if draft.Status != DirectorImageStagingCommitted {
					t.Fatalf("status must remain committed, got %v", draft.Status)
				}
				return
			}
			res := CommitDirectorImageStagingDraft(draft, tc.authHash, tc.at)
			if res.OK != tc.wantOK {
				t.Fatalf("ok = %v, want %v (%+v)", res.OK, tc.wantOK, res)
			}
			if tc.wantReason != "" && res.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", res.Reason, tc.wantReason)
			}
			if draft.Status != tc.wantStatus {
				t.Fatalf("status = %v, want %v", draft.Status, tc.wantStatus)
			}
			if !res.OK {
				if res.Scene != nil {
					t.Fatalf("failed commit must not return a scene, got %s", res.Scene)
				}
			} else {
				if len(res.Scene) == 0 {
					t.Fatal("successful commit must return the staged scene")
				}
			}
		})
	}
}

func TestDirectorImageStagingExpireBatch(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	fresh := stagingTestDraftID(t, "fresh-1", now, time.Hour)
	ApproveDirectorImageStagingDraft(fresh)
	expired := stagingTestDraftID(t, "exp-1", now.Add(-2*time.Minute), time.Minute)
	done := stagingTestDraftID(t, "done-1", now.Add(-2*time.Minute), time.Minute)
	ApproveDirectorImageStagingDraft(done)
	CommitDirectorImageStagingDraft(done, "hash-a", now.Add(-time.Minute))
	aborted := stagingTestDraftID(t, "abort-1", now.Add(-2*time.Minute), time.Minute)
	RejectDirectorImageStagingDraft(aborted)
	// 重复 id:首次出现为准(abort-1 的重复),其余草稿不受影响。
	duplicate := stagingTestDraftID(t, "abort-1", now.Add(-2*time.Minute), time.Minute)

	out := ExpireDirectorImageStagingDrafts([]*DirectorImageStagingDraft{fresh, expired, done, aborted, duplicate}, now)
	if len(out) != 4 {
		t.Fatalf("dedup by id must keep 4 drafts, got %d", len(out))
	}
	statusOf := map[*DirectorImageStagingDraft]DirectorImageStagingStatus{}
	for _, d := range out {
		statusOf[d] = d.Status
	}
	if statusOf[expired] != DirectorImageStagingRejected {
		t.Fatalf("expired approved draft must be rejected, got %v", statusOf[expired])
	}
	if statusOf[fresh] != DirectorImageStagingApproved {
		t.Fatalf("fresh draft must stay approved, got %v", statusOf[fresh])
	}
	if statusOf[done] != DirectorImageStagingCommitted {
		t.Fatalf("committed draft must stay committed, got %v", statusOf[done])
	}
	if statusOf[aborted] != DirectorImageStagingRejected {
		t.Fatalf("rejected draft must stay rejected, got %v", statusOf[aborted])
	}
}

func TestDirectorImageStagingReceipt(t *testing.T) {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	draft := stagingTestDraft(t, now, time.Minute)
	receipt := NewDirectorImageStagingReceipt("run-1", "call-7", draft)
	want := map[string]any{
		"requestId":  "run-1",
		"callId":     "call-7",
		"stagingId":  "stg-1",
		"sceneId":    "scene-1",
		"status":     "staging",
		"expiresAt":  draft.ExpiresAt.Format(time.RFC3339),
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse receipt: %v", err)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("receipt[%s] = %v, want %v (full: %s)", key, got[key], value, raw)
		}
	}
}