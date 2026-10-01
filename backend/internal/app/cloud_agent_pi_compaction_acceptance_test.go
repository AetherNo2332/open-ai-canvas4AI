package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
)

// Seed the SDK-shaped durable branch that the hook supplies as its source.
func seedPiCompactionMessages(t *testing.T, db *gorm.DB, run *model.CloudAgentExecution, messages []map[string]any) (int64, string) {
	t.Helper()
	var session model.CloudAgentPiSession
	if err := db.Where("user_id = ? AND conversation_id = ?", run.UserID, run.ConversationID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("session_id = ? AND user_id = ?", session.ID, run.UserID).Delete(&model.CloudAgentPiEntry{}).Error; err != nil {
		t.Fatal(err)
	}
	parentID := ""
	for index, message := range messages {
		id := fmt.Sprintf("pi-compact-message-%d", index+1)
		var parent any
		if parentID != "" {
			parent = parentID
		}
		raw, err := json.Marshal(map[string]any{"type": "message", "id": id, "parentId": parent,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": message})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.CloudAgentPiEntry{SessionID: session.ID, Sequence: index + 1,
			EntryID: id, ParentID: parentID, UserID: run.UserID, RunID: run.ID, EntryJSON: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
		parentID = id
	}
	revision := session.Revision + 1
	if err := db.Model(&session).Updates(map[string]any{"revision": revision, "active_leaf_id": parentID}).Error; err != nil {
		t.Fatal(err)
	}
	return revision, parentID
}

func TestPiContextCompactionGoWorkerSettlesOneModelCall(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("REDIS_URL", "")
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["stream"] == true || len(creationMaps(body["tools"])) > 0 {
			t.Error("Go structured compactor exposed tools or used streaming")
		}
		checkpoint, _ := json.Marshal(agentcontext.Checkpoint{Version: agentcontext.Version,
			HistorySummary: "worker-produced structured history", ScriptDesign: "保持伤口连续性",
			CurrentWork: "推进当前分镜", NextStep: "重新读取画布"})
		response := map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": string(checkpoint)}, "finish_reason": "stop",
		}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer upstream.Close()
	s, db, run, _, _ := smallWindowCompactionFixture(t)
	s = New(s.repo, s.dataDir)
	t.Cleanup(func() { _ = s.Close() })
	if err := db.Model(&model.ModelChannel{}).Where("id = ?", "channel").
		Updates(map[string]any{"base_url": upstream.URL, "api_key": "test-only"}).Error; err != nil {
		t.Fatal(err)
	}
	operation := beginPiCompactionForTest(t, s, run, "manual")
	session, _, _ := s.repo.CloudAgentPiSession("user", run.ConversationID)
	request := PiContextCompactionStart{SessionRevision: session.Revision, ActiveLeafID: session.ActiveLeafID,
		Reason: "manual", TokensBefore: 24000}
	retry, err := s.PiBeginContextCompaction("user", run.ID, run.LeaseOwner, request)
	if err != nil || retry.TaskID != operation.TaskID {
		t.Fatalf("begin retry was not exactly once: retry=%+v err=%v", retry, err)
	}
	if err := s.ProcessNextTask(); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessNextTask(); err != nil {
		t.Fatal(err)
	}
	ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
	if ready.Fallback || ready.Mode != "model" || requests.Load() != 1 {
		t.Fatalf("Go worker did not produce exactly one successful compaction: ready=%+v calls=%d", ready, requests.Load())
	}
	input := piCompactionCommitForTest(t, s, run, ready, "worker-compaction")
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := s.PiCommitContextCompaction("user", run.ID, run.LeaseOwner, operation.OperationID, input); err != nil {
			t.Fatal(err)
		}
	}
	var orders []model.BillingOrder
	if err := db.Where("task_id = ?", operation.TaskID).Find(&orders).Error; err != nil || len(orders) != 1 ||
		orders[0].Status != model.BillingStatusSettled || orders[0].ActualAmountMicrocredits <= 0 {
		t.Fatalf("worker compaction was not billed once: orders=%+v err=%v", orders, err)
	}
	var account model.CreditAccount
	if err := db.Where("user_id = ?", "user").First(&account).Error; err != nil || account.ReservedMicrocredits != 0 {
		t.Fatalf("settled compaction retained reserved balance: account=%+v err=%v", account, err)
	}
	var calls int64
	if err := db.Model(&model.ApiCallLog{}).Where("task_id = ?", operation.TaskID).Count(&calls).Error; err != nil || calls != 1 {
		t.Fatalf("compaction retry duplicated usage records: calls=%d err=%v", calls, err)
	}
	var settled int64
	if err := db.Model(&model.CreditLedgerEntry{}).Where("billing_order_id = ? AND type = ?", orders[0].ID, model.CreditLedgerConsume).Count(&settled).Error; err != nil || settled != 1 {
		t.Fatalf("compaction retry duplicated settlement entries: entries=%d err=%v", settled, err)
	}
}

func TestPiCompactionRestoresOnlyDurablyDeliveredInterjectionSources(t *testing.T) {
	s, db, run, state, _ := smallWindowCompactionFixture(t)
	state.InterjectionIDs = []string{"verified-interjection"}
	state.event(run.ID, "user_interjection_delivered", map[string]any{"messageId": "verified-interjection", "text": "保持冷色调"})
	state.event(run.ID, "user_interjection_delivered", map[string]any{"messageId": "uncommitted-id", "text": "不存在的确认"})
	run = saveCloudAgentCompactionState(t, s, run, &state)
	state.Canonical.Messages = append(state.Canonical.Messages,
		map[string]any{"role": "user", "content": "【用户插话】保持冷色调"},
		map[string]any{"role": "user", "content": "【用户插话】不存在的确认"},
		map[string]any{"role": "user", "content": "【用户插话】未送达的文本"},
	)
	seedPiCompactionMessages(t, db, run, state.Canonical.Messages)
	beginPiCompactionForTest(t, s, run, "threshold")
	_, pending := reloadPiRun(t, s, run.ID)
	var restored int
	for _, message := range pending.Canonical.Messages {
		if stringField(message, cloudAgentContextSourceKey) == "user_interjection" {
			restored++
			if stringField(message, "content") != "【用户插话】保持冷色调" {
				t.Fatalf("unverified user text acquired delivery authority: %+v", message)
			}
		}
	}
	if restored != 1 {
		t.Fatalf("durable delivery identity was lost during compaction projection: %d", restored)
	}
}

func beginPiCompactionForTest(t *testing.T, s *Service, run *model.CloudAgentExecution, reason string) *PiContextCompactionView {
	t.Helper()
	session, _, err := s.repo.CloudAgentPiSession(run.UserID, run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := s.PiBeginContextCompaction(run.UserID, run.ID, run.LeaseOwner,
		PiContextCompactionStart{SessionRevision: session.Revision, ActiveLeafID: session.ActiveLeafID,
			Reason: reason, WillRetry: reason == "overflow", TokensBefore: 24000})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func setPiCompactionTaskResult(t *testing.T, db *gorm.DB, taskID string, status model.TaskStatus, result string) {
	t.Helper()
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).
		Updates(map[string]any{"status": status, "result_json": result}).Error; err != nil {
		t.Fatal(err)
	}
}

func queryPiCompactionForTest(t *testing.T, s *Service, run *model.CloudAgentExecution, operationID string) *PiContextCompactionView {
	t.Helper()
	ready, err := s.PiContextCompaction(run.UserID, run.ID, run.LeaseOwner, operationID)
	if err != nil || ready.Status != "succeeded" || ready.Summary == "" {
		t.Fatalf("Go compaction did not yield a checkpoint: ready=%+v err=%v", ready, err)
	}
	return ready
}

func piCompactionCommitForTest(t *testing.T, s *Service, run *model.CloudAgentExecution, ready *PiContextCompactionView, id string) PiContextCompactionCommit {
	t.Helper()
	_, state := reloadPiRun(t, s, run.ID)
	compaction := state.ContextCompaction
	if compaction == nil {
		t.Fatal("missing pending Pi compaction source")
	}
	raw, err := json.Marshal(map[string]any{"type": "compaction", "id": id,
		"parentId": compaction.PiSourceLeafID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"summary": ready.Summary, "firstKeptEntryId": ready.FirstKeptEntryID, "tokensBefore": ready.TokensBefore,
		"details": ready.Details, "fromHook": true})
	if err != nil {
		t.Fatal(err)
	}
	return PiContextCompactionCommit{SessionRevision: compaction.PiSessionRevision, Entry: raw}
}

func commitPiCompactionForTest(t *testing.T, s *Service, run *model.CloudAgentExecution, ready *PiContextCompactionView, id string) {
	t.Helper()
	input := piCompactionCommitForTest(t, s, run, ready, id)
	revision, err := s.PiCommitContextCompaction(run.UserID, run.ID, run.LeaseOwner, ready.OperationID, input)
	if err != nil || revision != input.SessionRevision+1 {
		t.Fatalf("Pi checkpoint commit failed: revision=%d err=%v", revision, err)
	}
}

func TestPiContextCompactionRejectsSourceIdentityDrift(t *testing.T) {
	for _, kind := range []string{"revision", "leaf", "run", "entry-parent", "entry-digest", "entry-summary", "entry-boundary"} {
		t.Run(kind, func(t *testing.T) {
			s, db, run, _, _ := smallWindowCompactionFixture(t)
			operation := beginPiCompactionForTest(t, s, run, "threshold")
			setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusFailed, "{}")
			ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
			input := piCompactionCommitForTest(t, s, run, ready, "conflicted-compaction")
			var sessionChanges map[string]any
			switch kind {
			case "revision":
				sessionChanges = map[string]any{"revision": input.SessionRevision + 1}
			case "leaf":
				sessionChanges = map[string]any{"active_leaf_id": "pi-compact-message-1"}
			case "run":
				sessionChanges = map[string]any{"active_run_id": "another-run"}
			default:
				var entry map[string]any
				if err := json.Unmarshal(input.Entry, &entry); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "entry-parent":
					entry["parentId"] = "pi-compact-message-1"
				case "entry-digest":
					entry["details"].(map[string]any)["sourceDigest"] = "forged"
				case "entry-summary":
					entry["summary"] = ready.Summary + "forged"
				case "entry-boundary":
					entry["firstKeptEntryId"] = "pi-compact-message-1"
				}
				input.Entry, _ = json.Marshal(entry)
			}
			if sessionChanges != nil {
				if err := db.Model(&model.CloudAgentPiSession{}).Where("conversation_id = ? AND user_id = ?", run.ConversationID, run.UserID).Updates(sessionChanges).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.PiCommitContextCompaction(run.UserID, run.ID, run.LeaseOwner, ready.OperationID, input); err == nil {
				t.Fatal("source identity drift permitted a stale checkpoint commit")
			}
			_, state := reloadPiRun(t, s, run.ID)
			if state.ContextCompaction == nil || state.ContextCheckpoint != nil || state.ContextCompactionCount != 0 {
				t.Fatalf("rejected commit changed canonical state: %+v", state)
			}
			var committed int64
			if err := db.Model(&model.CloudAgentPiEntry{}).Where("entry_id = ?", "conflicted-compaction").Count(&committed).Error; err != nil || committed != 0 {
				t.Fatalf("rejected commit leaked a Pi entry: %d, %v", committed, err)
			}
		})
	}
}

func TestPiContextCompactionRecoversAfterLeaseTakeoverWithoutNewCharge(t *testing.T) {
	s, db, run, _, _ := smallWindowCompactionFixture(t)
	operation := beginPiCompactionForTest(t, s, run, "overflow")
	var ordersBefore int64
	if err := db.Model(&model.BillingOrder{}).Where("user_id = ?", run.UserID).Count(&ordersBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).Where("conversation_id = ? AND user_id = ?", run.ConversationID, run.UserID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ClaimPiAgent("worker-recovered")
	if err != nil || recovered == nil || recovered.RunID != run.ID || recovered.PendingContextCompaction == nil ||
		recovered.PendingContextCompaction.OperationID != operation.OperationID {
		t.Fatalf("lease takeover did not restore pending Go operation: snapshot=%+v err=%v", recovered, err)
	}
	if _, err := s.PiContextCompaction(run.UserID, run.ID, run.LeaseOwner, operation.OperationID); err == nil {
		t.Fatal("old worker retained compaction authority after lease takeover")
	}
	run.LeaseOwner = "worker-recovered"
	resumed, err := s.PiBeginContextCompaction(run.UserID, run.ID, run.LeaseOwner, PiContextCompactionStart{
		SessionRevision: recovered.PendingContextCompaction.SessionRevision, ActiveLeafID: recovered.PendingContextCompaction.ActiveLeafID,
		Reason: "overflow", WillRetry: true, TokensBefore: 24000})
	if err != nil || resumed.OperationID != operation.OperationID || resumed.TaskID != operation.TaskID {
		t.Fatalf("resume duplicated the task: operation=%+v err=%v", resumed, err)
	}
	var ordersAfter int64
	if err := db.Model(&model.BillingOrder{}).Where("user_id = ?", run.UserID).Count(&ordersAfter).Error; err != nil || ordersBefore != ordersAfter {
		t.Fatalf("retry created another bill: before=%d after=%d err=%v", ordersBefore, ordersAfter, err)
	}
	beforeRelease, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.ReleasePiAgentLease("user", run.ID, run.LeaseOwner, beforeRelease.LeaseEpoch); err != nil {
		t.Fatal(err)
	}
	afterRelease, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil || afterRelease.Revision != beforeRelease.Revision {
		t.Fatalf("lease release changed compaction source revision: before=%+v after=%+v err=%v", beforeRelease, afterRelease, err)
	}
	reclaimed, err := s.ClaimPiAgent("worker-after-release")
	if err != nil || reclaimed == nil || reclaimed.PendingContextCompaction == nil ||
		reclaimed.PiSessionRevision != beforeRelease.Revision ||
		reclaimed.PendingContextCompaction.SessionRevision != reclaimed.PiSessionRevision {
		t.Fatalf("graceful worker release did not preserve pending compaction source: snapshot=%+v err=%v", reclaimed, err)
	}
	run.LeaseOwner = "worker-after-release"
	setPiCompactionTaskResult(t, db, operation.TaskID, model.TaskStatusFailed, "{}")
	ready := queryPiCompactionForTest(t, s, run, operation.OperationID)
	input := piCompactionCommitForTest(t, s, run, ready, "recovered-compaction")
	first, err := s.PiCommitContextCompaction(run.UserID, run.ID, run.LeaseOwner, operation.OperationID, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.PiCommitContextCompaction(run.UserID, run.ID, run.LeaseOwner, operation.OperationID, input)
	if err != nil || second != first {
		t.Fatalf("recovered commit is not idempotent: first=%d second=%d err=%v", first, second, err)
	}
	var conflicting map[string]any
	json.Unmarshal(input.Entry, &conflicting)
	conflicting["id"] = "other-entry"
	input.Entry, _ = json.Marshal(conflicting)
	if _, err := s.PiCommitContextCompaction(run.UserID, run.ID, run.LeaseOwner, operation.OperationID, input); err == nil {
		t.Fatal("idempotent retry accepted a different Pi entry identity")
	}
}
