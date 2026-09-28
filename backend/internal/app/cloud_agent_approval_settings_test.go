package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestCloudAgentMediaReadHashSurvivesMoveBeforeDraft(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	view, err := cloudAgentCanvasState(s.repo, "user", "agent-canvas", doc, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	args.SnapshotHash = view.(map[string]any)["mediaSnapshotHash"].(string)
	nodes, _ := creationObjects(doc["nodes"])
	nodes["cat"]["position"] = map[string]any{"x": 1600, "y": 500}
	raw, _ := json.Marshal(doc)
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	run, _ := agentMediaRun(t, s, args, "request_approval")
	if _, err := s.PiToolAdvance("user", run.ID, "worker-a", agentPiToolTaskID(t, s, run.ID), "media-call"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.CloudAgentRun("user", run.ID)
	if err != nil || pending.Status != "waiting_approval" {
		t.Fatalf("movement prevented draft creation: %v %+v", err, pending)
	}
	approveAgentMediaDraft(t, s, run.ID)
}

func TestCloudAgentMediaApprovalAllowsMovesButRejectsContentChanges(t *testing.T) {
	for _, change := range []string{"move", "presentation", "prompt", "resource", "connection", "locked", "task", "unknown_metadata"} {
		t.Run(change, func(t *testing.T) {
			s, db, a := agentMediaFixture(t)
			run, _ := agentMediaRun(t, s, a, "request_approval")
			if _, err := s.PiToolAdvance("user", run.ID, "worker-a", agentPiToolTaskID(t, s, run.ID), "media-call"); err != nil {
				t.Fatal(err)
			}
			run, _ = s.repo.CloudAgent("user", run.ID)
			state, err := cloudAgentDecode(run)
			if err != nil || state.Approval == nil {
				t.Fatalf("missing approval: %v", err)
			}
			canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
			doc, _ := creationDocument(canvas.PayloadJSON)
			nodes, _ := creationObjects(doc["nodes"])
			fullHash := cloudAgentCanvasHash(doc)
			switch change {
			case "move":
				nodes["cat"]["position"] = map[string]any{"x": 1234, "y": 567}
				nodes[a.NodeID]["position"] = map[string]any{"x": 2000, "y": 900}
			case "presentation":
				nodes[a.NodeID]["width"], nodes[a.NodeID]["height"] = 500, 600
				nodes[a.NodeID]["createdAt"], nodes[a.NodeID]["updatedAt"] = "2026-09-19T00:00:00Z", "2026-09-19T00:00:01Z"
				doc["chatMessages"] = []any{map[string]any{"text": "用户补充消息"}}
			case "prompt":
				nodes[a.NodeID]["metadata"].(map[string]any)["composerContent"] = "用户修改的提示词"
			case "resource":
				nodes["cat"]["metadata"].(map[string]any)["storageKey"] = "resource:ref-two"
			case "connection":
				doc["connections"] = []any{}
			case "locked":
				nodes[a.NodeID]["metadata"].(map[string]any)["locked"] = true
			case "task":
				nodes[a.NodeID]["metadata"].(map[string]any)["taskId"] = "another-task"
			case "unknown_metadata":
				nodes[a.NodeID]["metadata"].(map[string]any)["futureGenerationInput"] = "changed"
			}
			if fullHash == cloudAgentCanvasHash(doc) {
				t.Fatal("full mutation/undo hash must still detect changes")
			}
			raw, _ := json.Marshal(doc)
			if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.DecideCloudAgentApproval("user", run.ID, state.Approval.ID, "approve", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PiToolAdvance("user", run.ID, "worker-a", agentPiToolTaskID(t, s, run.ID), "media-call"); err != nil {
				t.Fatal(err)
			}
			var count int64
			db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count)
			if change != "move" && change != "presentation" {
				if count != 0 {
					t.Fatal("content change submitted generation")
				}
				return
			}
			if count != 1 {
				t.Fatal("layout-only change blocked generation")
			}
			canvas, _ = s.repo.CanvasProjectForUser("user", "agent-canvas")
			doc, _ = creationDocument(canvas.PayloadJSON)
			nodes, _ = creationObjects(doc["nodes"])
			if change == "move" && (nodes["cat"]["position"].(map[string]any)["x"] != float64(1234) || nodes[a.NodeID]["position"].(map[string]any)["x"] != float64(2000)) {
				t.Fatal("generation overwrote user's positions")
			}
			if change == "presentation" && (nodes[a.NodeID]["width"] != float64(500) || nodes[a.NodeID]["height"] != float64(600) || nodes[a.NodeID]["createdAt"] != "2026-09-19T00:00:00Z" || nodes[a.NodeID]["updatedAt"] != "2026-09-19T00:00:01Z" || len(doc["chatMessages"].([]any)) != 1) {
				t.Fatal("generation overwrote presentation autosave")
			}
		})
	}
}

func TestCloudAgentMediaApprovalReusesPreparedInputsAfterModelRetry(t *testing.T) {
	s, db, a := agentMediaFixture(t)
	run, _ := agentMediaRun(t, s, a, "request_approval")
	if _, err := s.PiToolAdvance("user", run.ID, "worker-a", agentPiToolTaskID(t, s, run.ID), "media-call"); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, err := cloudAgentDecode(run)
	if err != nil || state.Approval == nil || state.Approval.Prepared == nil {
		t.Fatalf("missing prepared media approval: %v", err)
	}

	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := creationObjects(doc["nodes"])
	if err != nil {
		t.Fatal(err)
	}
	nodes[a.NodeID]["position"] = map[string]any{"x": 1800.0, "y": 920.0}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}

	retried := state.Approval.Call
	retried.ID = "media-call-retry"
	_, plan, err := s.prepareCloudAgentMedia(run, &state, retried)
	if err != nil {
		t.Fatalf("layout-only edit plus regenerated tool-call ID must reuse prepared media: %v", err)
	}
	if plan == nil {
		t.Fatal("retry did not retain prepared media admission")
	}
}

func TestCloudAgentImageApprovalEditsAreValidatedAndIdempotent(t *testing.T) {
	s, db, a := agentMediaFixture(t)
	capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceGrokImage), "grok-image")
	for _, key := range []string{"grok-image", "grok-image-alternative"} {
		for _, row := range []any{
			&model.ChannelModel{ID: key, ChannelID: "channel", ModelKey: key, DisplayName: key, Capability: "image", Protocol: model.ChannelInterfaceGrokImage, CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, capability), BillingMode: "fixed_request", PriceConfigured: true, Enabled: true},
			&model.ChannelModelPriceTier{ID: key + "-tier", ChannelModelID: key, SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 1, PriceConfigured: true, Enabled: true},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	a.Mode, a.ChannelModelKey, a.Duration, a.VideoGenerateAudio = "image", "grok-image", 0, nil
	a.Size, a.Quality, a.NodeID = "1:1", "2k", "image-shot-1"
	a.ReferenceNodeIDs = []string{"cat"}
	run, _ := agentMediaRun(t, s, a, "request_approval")
	if _, err := s.PiToolAdvance("user", run.ID, "worker-a", agentPiToolTaskID(t, s, run.ID), "media-call"); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, err := cloudAgentDecode(run)
	if err != nil || state.Approval == nil {
		t.Fatalf("missing approval: %v", err)
	}
	var approvedArgs cloudAgentMediaArgs
	if err := json.Unmarshal([]byte(state.Approval.Call.Function.Arguments), &approvedArgs); err != nil {
		t.Fatal(err)
	}
	id := state.Approval.ID
	settings := CloudAgentMediaSettings{ChannelID: "channel", ChannelModelKey: "grok-image-alternative", Size: "16:9", Quality: "1k"}
	for _, invalid := range []CloudAgentMediaSettings{
		{ChannelID: "channel", ChannelModelKey: "missing", Size: "16:9", Quality: "1k"},
		{ChannelID: "channel", ChannelModelKey: "text-test", Size: "16:9", Quality: "1k"},
		{LogicalModelID: "mixed", ChannelID: "channel", ChannelModelKey: settings.ChannelModelKey, Size: "16:9"},
		{ChannelID: "channel", ChannelModelKey: settings.ChannelModelKey, Size: "invalid-size", Quality: "1k"},
		{ChannelID: "channel", ChannelModelKey: settings.ChannelModelKey, Size: "16:9", Quality: "invalid-quality"},
	} {
		if err := s.DecideCloudAgentApproval("user", run.ID, id, "approve", "", &invalid); err == nil {
			t.Fatalf("invalid settings accepted: %+v", invalid)
		}
	}
	if err := s.DecideCloudAgentApproval("other", run.ID, id, "approve", "", &settings); err == nil {
		t.Fatal("cross-user approval accepted")
	}
	pending, _ := s.CloudAgentRun("user", run.ID)
	if pending.Status != "waiting_approval" {
		t.Fatal("invalid settings consumed approval")
	}
	var before int64
	db.Model(&model.BillingOrder{}).Count(&before)
	for range 2 {
		if err := s.DecideCloudAgentApproval("user", run.ID, id, "approve", "", &settings); err != nil {
			t.Fatal(err)
		}
	}
	var orders int64
	db.Model(&model.BillingOrder{}).Count(&orders)
	if orders != before {
		t.Fatal("editing approval charged before task submission")
	}
	changed := settings
	changed.Size = "1:1"
	if err := s.DecideCloudAgentApproval("user", run.ID, id, "approve", "", &changed); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	if _, err := s.PiToolAdvance("user", run.ID, "worker-a", agentPiToolTaskID(t, s, run.ID), "media-call"); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, err = cloudAgentDecode(run)
	if err != nil || state.MediaTaskID == "" {
		t.Fatalf("edited approval did not submit: %v %+v", err, state.Events)
	}
	task, err := s.repo.TaskForUser("user", state.MediaTaskID)
	if err != nil {
		t.Fatal(err)
	}
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input.Config.Model != settings.ChannelModelKey || input.Config.Size != settings.Size || input.Config.Quality != settings.Quality || task.Prompt != approvedArgs.Prompt || len(input.ReferenceImages) != 1 {
		t.Fatalf("approved inputs were lost: %+v", input.Config)
	}
	var count int64
	db.Model(&model.Task{}).Where("type = ?", "canvas_image").Count(&count)
	if count != 1 {
		t.Fatalf("expected exactly one image task, got %d", count)
	}
}

func TestCloudAgentConversationKeepsRecentRounds(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	parent := ""
	activeLeaf := ""
	for round := 0; round < 15; round++ {
		req := agentTestRequest()
		req.IdempotencyKey = fmt.Sprintf("long-conversation-%d", round)
		run, err := s.CreateCloudAgentRun("user", req, parent)
		if err != nil {
			t.Fatalf("round %d failed: %v", round+1, err)
		}
		execution, _ := s.repo.CloudAgent("user", run.ID)
		state, err := cloudAgentDecode(execution)
		if err != nil {
			t.Fatalf("history decode failed at round %d: %v", round, err)
		}
		want := round * 2
		if want > cloudAgentHistoryKeepRounds*2 {
			want = cloudAgentHistoryKeepRounds * 2
		}
		if len(state.TextHistory) != want {
			t.Fatalf("history window at round %d: got %d want %d", round, len(state.TextHistory), want)
		}
		if round > 0 {
			previousTurn := state.TextHistory[len(state.TextHistory)-2:]
			if previousTurn[0].Role != "user" || previousTurn[0].Content != req.Prompt || previousTurn[1].Role != "assistant" || previousTurn[1].Content != "继续创作" {
				t.Fatalf("Pi continuation lost the previous user/assistant turn at round %d: %+v", round, previousTurn)
			}
		}
		claimed, err := s.ClaimPiAgent(fmt.Sprintf("history-worker-%d", round))
		if err != nil || claimed == nil || claimed.RunID != run.ID {
			t.Fatalf("Pi run %d was not claimed: snapshot=%#v error=%v", round, claimed, err)
		}
		leased, err := s.repo.CloudAgent("user", run.ID)
		if err != nil {
			t.Fatal(err)
		}
		for index, role := range []string{"user", "assistant"} {
			id := fmt.Sprintf("round-%02d-%s", round, role)
			messageText := req.Prompt
			if role == "assistant" {
				messageText = "继续创作"
			}
			message, _ := json.Marshal(map[string]any{"role": role, "content": messageText, "timestamp": round*2 + index + 1})
			parentEntry := activeLeaf
			var parentValue any
			if parentEntry != "" {
				parentValue = parentEntry
			}
			entry, _ := json.Marshal(map[string]any{
				"type": "message", "id": id, "parentId": parentValue,
				"timestamp": fmt.Sprintf("2026-09-28T00:%02d:%02dZ", round, index), "message": json.RawMessage(message),
			})
			session, _, err := s.repo.CloudAgentPiSession("user", execution.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := s.PiCheckpointMessageResult("user", run.ID, leased.LeaseOwner, PiMessageCheckpoint{
				Sequence: index + 1, Message: message, SessionRevision: session.Revision,
				ActiveLeafID: id, SessionEntries: []json.RawMessage{entry},
			})
			if err != nil {
				t.Fatalf("Pi message checkpoint %s failed: %v", id, err)
			}
			activeLeaf = id
			if revision != session.Revision+1 {
				t.Fatalf("Pi session revision after %s = %d, want %d", id, revision, session.Revision+1)
			}
			leased, err = s.repo.CloudAgent("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := s.repo.MutateCloudAgent("user", run.ID, leased.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			currentState, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			currentState.Canonical.Messages = append(currentState.Canonical.Messages, map[string]any{"role": "assistant", "content": "继续创作"})
			currentState.event(run.ID, "assistant_message", map[string]any{"messageId": run.ID + ":final", "text": "继续创作", "final": true})
			current.Status = "completed"
			return cloudAgentSave(current, &currentState)
		}); err != nil {
			t.Fatalf("complete Pi conversation round %d: %v", round, err)
		}
		parent = run.ID
	}
	run, _ := s.repo.CloudAgent("user", parent)
	state, _ := cloudAgentDecode(run)
	_, entries, err := s.repo.CloudAgentPiSession("user", run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 30 {
		t.Fatalf("durable Pi conversation entries = %d, want 30", len(entries))
	}
	for i := range state.TextHistory {
		state.TextHistory[i].Content = strings.Repeat("x", 4000)
	}
	raw, _ := json.Marshal(state)
	if err := db.Model(run).Update("state_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.IdempotencyKey = "long-conversation-trim-bytes"
	child, err := s.CreateCloudAgentRun("user", req, parent)
	if err != nil {
		t.Fatalf("oversize history should trim not fail: %v", err)
	}
	childRun, err := s.repo.CloudAgent("user", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	childState, err := cloudAgentDecode(childRun)
	if err != nil {
		t.Fatal(err)
	}
	if cloudAgentHistoryJSONSize(childState.TextHistory) > cloudAgentHistoryMaxBytes {
		t.Fatal("trimmed history still over cap")
	}
	if cloudAgentHistoryUserInstructionCount(childState.TextHistory) < 1 {
		t.Fatal("trimmed away the conversation")
	}
}
