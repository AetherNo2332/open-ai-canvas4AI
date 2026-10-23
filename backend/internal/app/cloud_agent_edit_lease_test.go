package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestAgentExecutorLeaseConflictsRollbackEffectsThenPersistBusinessReceipt(t *testing.T) {
	for _, reason := range []string{"node_locked", "edit_proof_invalid", "infrastructure"} {
		t.Run(reason, func(t *testing.T) {
			s, db, _, _ := creationTestService(t)
			row := &model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"id":"agent-canvas","nodes":[{"id":"text","type":"text","metadata":{"content":"original"}}],"connections":[]}`}
			if err := s.repo.UpsertCanvasProject(row); err != nil {
				t.Fatal(err)
			}
			req := agentTestRequest()
			req.PermissionMode = "auto"
			view, err := s.CreateCloudAgentRun("user", req, "")
			if err != nil {
				t.Fatal(err)
			}
			grant, err := s.AcquireCanvasEditLease(context.Background(), "user", row.ID, CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
			if err != nil {
				t.Fatal(err)
			}
			if reason == "edit_proof_invalid" {
				proof := grant.CanvasEditProof
				proof.Token = strings.Repeat("b", 48)
				s.repo = s.repo.WithContext(repository.WithCanvasEditProofs(context.Background(), []repository.CanvasEditProof{proof}))
			}
			doc, _ := creationDocument(row.PayloadJSON)
			call := cloudAgentCall{ID: "leased-write"}
			call.Function.Name = "canvas_apply_ops"
			call.Function.Arguments = mustCanvasSyncJSON(t, map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "ops": []map[string]any{{"type": "update_node", "id": "text", "patch": map[string]any{"content": "Agent changes"}}}})
			legal := cloudAgentCall{ID: "legal-read"}
			legal.Function.Name = "canvas_get_state"
			legal.Function.Arguments = `{}`
			run, state := agentStagePiOutputForTest(t, s, db, view.ID, "", []cloudAgentCall{call, legal})
			// Fault injection at the real final guard: speculative tool rows already
			// exist in its transaction. A classify-and-swallow fix would commit them.
			injected := false
			infraErr := errors.New("injected tool database failure")
			if err = db.Callback().Query().Before("gorm:query").Register("test:lease-tool-effects", func(tx *gorm.DB) {
				if tx.Statement.Table != "canvas_node_edit_leases" || injected {
					return
				}
				injected = true
				writes := tx.Session(&gorm.Session{NewDB: true})
				for _, value := range []any{&model.Resource{ID: "lease-spec-resource", UserID: "user"}, &model.Asset{ID: "lease-spec-asset", UserID: "user", PayloadJSON: "{}"}, &model.BillingOrder{ID: "lease-spec-bill", UserID: "user", TaskID: "lease-spec-task"}} {
					if err := writes.Create(value).Error; err != nil {
						tx.AddError(err)
						return
					}
				}
				if reason == "infrastructure" {
					tx.AddError(infraErr)
				}
			}); err != nil {
				t.Fatal(err)
			}
			receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, call.ID)
			if reason == "infrastructure" {
				if !errors.Is(err, infraErr) {
					t.Fatalf("database failure converted to business result: %v", err)
				}
				after, _ := s.repo.CloudAgent("user", run.ID)
				if after.Revision != run.Revision || after.EventCount != run.EventCount {
					t.Fatal("infrastructure failure wrote receipt")
				}
				for _, value := range []any{&model.Resource{}, &model.Asset{}, &model.BillingOrder{}} {
					var count int64
					if err = db.Model(value).Where("id LIKE ?", "lease-spec-%").Count(&count).Error; err != nil || count != 0 {
						t.Fatalf("infrastructure failure kept effects: %T %d %v", value, count, err)
					}
				}
				return
			}
			if err != nil || receipt == nil || receipt.Pending {
				t.Fatalf("executor infrastructure error for normal lease conflict: %v", err)
			}
			after, err := s.repo.CloudAgent("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := cloudAgentDecode(after)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range persisted.Events {
				if event.Type == "tool_failed" && event.Payload["callId"] == call.ID {
					raw, _ := json.Marshal(event.Payload)
					var payload map[string]any
					_ = json.Unmarshal(raw, &payload)
					result, _ := payload["result"].(map[string]any)
					if result["reason"] != reason || result["nodeId"] != "text" || result["errorClass"] != "state_conflict" || result["retryable"] != false || result["requiredAction"] == "" {
						t.Fatalf("unstructured conflict: %s", raw)
					}
					found = true
				}
			}
			if !found || persisted.CallIndex != 1 {
				t.Fatalf("missing durable conflict receipt: found=%v index=%d", found, persisted.CallIndex)
			}
			replayed, replayErr := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, call.ID)
			if replayErr != nil || !reflect.DeepEqual(receipt, replayed) {
				t.Fatalf("conflict receipt did not replay: %v", replayErr)
			}
			unchanged, _ := s.repo.CloudAgent("user", run.ID)
			if unchanged.Revision != after.Revision || unchanged.EventCount != after.EventCount {
				t.Fatal("duplicate conflict wrote another receipt")
			}
			stored, _ := s.repo.CanvasProjectForUser("user", row.ID)
			if stored.PayloadJSON != row.PayloadJSON || stored.Revision != row.Revision {
				t.Fatal("failed tool changed canvas")
			}
			for _, value := range []any{&model.Resource{}, &model.Asset{}, &model.BillingOrder{}} {
				var count int64
				if err = db.Model(value).Where("id LIKE ?", "lease-spec-%").Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("speculative side effect survived: %T %d %v", value, count, err)
				}
			}
			if !injected {
				t.Fatal("test did not reach actual persistence guard")
			}
			if _, err = s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, legal.ID); err != nil {
				t.Fatalf("other legal tool cannot continue: %v", err)
			}
			latest, _ := s.repo.CloudAgent("user", run.ID)
			finished, _ := cloudAgentDecode(latest)
			if finished.CallIndex != 2 || latest.Status == "failed" {
				t.Fatalf("run did not continue: %s index=%d", latest.Status, finished.CallIndex)
			}
		})
	}
}

func TestAgentLeaseRejectionReceiptCASPreservesOtherCommittedCall(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := s.repo.UpsertCanvasProject(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[],"connections":[]}`}); err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.PermissionMode = "auto"
	view, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "read"}
	call.Function.Name = "canvas_get_state"
	call.Function.Arguments = `{}`
	stale, state := agentStagePiOutputForTest(t, s, db, view.ID, "", []cloudAgentCall{call})
	// Another legitimate call commits in the gap after a failed tool rollback.
	if err = s.repo.MutateCloudAgent("user", stale.ID, stale.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		latest, decodeErr := cloudAgentDecode(current)
		if decodeErr != nil {
			return decodeErr
		}
		latest.event(stale.ID, "other_observation", map[string]any{"text": "must remain committed"})
		return cloudAgentSave(current, &latest)
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.repo.CloudAgent("user", stale.ID)
	lease := &repository.CanvasEditLeaseError{Reason: "node_locked", CanvasID: "agent-canvas", NodeID: "text"}
	if err = s.persistCloudAgentLeaseRejection(stale, &state, call, lease); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("stale receipt did not request reread: %v", err)
	}
	after, _ := s.repo.CloudAgent("user", stale.ID)
	if after.Revision != before.Revision || after.StateJSON != before.StateJSON || after.EventCount != before.EventCount {
		t.Fatal("stale receipt overwrote another committed call")
	}
}

func TestAgentCharacterLeaseConflictRollsBackActualCreatedAsset(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	doc := map[string]any{"id": "agent-canvas", "referenceFieldsVersion": 1, "nodes": []any{
		map[string]any{"id": "portrait", "type": "image", "metadata": map[string]any{"storageKey": "resource:portrait-res"}},
		map[string]any{"id": "card", "type": "text", "metadata": map[string]any{"content": "old card"}},
	}, "connections": []any{}}
	if err := db.Create(&model.Resource{ID: "portrait-res", UserID: "user", Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Size: 10, Width: 720, Height: 960}).Error; err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: mustCanvasSyncJSON(t, doc)}
	if err := s.repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	grant, err := s.AcquireCanvasEditLease(context.Background(), "user", row.ID, CanvasEditProof{NodeID: "card", SessionID: "tab", Token: strings.Repeat("a", 48)})
	if err != nil {
		t.Fatal(err)
	}
	doc["nodes"] = doc["nodes"].([]any)[:1]
	row.PayloadJSON = mustCanvasSyncJSON(t, doc)
	ctx := repository.WithCanvasEditProofs(context.Background(), []repository.CanvasEditProof{grant.CanvasEditProof})
	if err = s.repo.WithContext(ctx).UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Prompt = "请根据肖像创建角色卡"
	view, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentStoryboardCall(t, "canvas_create_character", "create-card", map[string]any{"nodeId": "card", "name": "Hero", "imageNodeId": "portrait"})
	run, state := agentStagePiOutputForTest(t, s, db, view.ID, "", []cloudAgentCall{call})
	created := false
	if err = db.Callback().Create().After("gorm:create").Register("test:character-before-lease", func(tx *gorm.DB) {
		if tx.Statement.Table == "assets" && tx.RowsAffected > 0 {
			created = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, call.ID)
	if err != nil || receipt == nil || receipt.Pending {
		t.Fatalf("character executor: %+v %v", receipt, err)
	}
	if !created {
		t.Fatalf("test never reached actual character asset creation: %+v", receipt)
	}
	for _, value := range []any{&model.Asset{}, &model.AssetVersion{}, &model.AssetRepresentation{}} {
		var count int64
		if err = db.Model(value).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("character tool kept asset effects: %T %d %v", value, count, err)
		}
	}
	latest, _ := s.repo.CloudAgent("user", run.ID)
	finished, _ := cloudAgentDecode(latest)
	found := false
	for _, event := range finished.Events {
		if event.Type == "tool_failed" && event.Payload["callId"] == call.ID {
			result, _ := event.Payload["result"].(map[string]any)
			found = result["reason"] == "node_locked"
		}
	}
	if !found {
		t.Fatal("actual character rejection has no structured lease receipt")
	}
}
