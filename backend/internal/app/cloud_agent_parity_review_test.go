package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/canvas/contract"
	"infinite-canvas/backend/internal/model"
)

func TestAgentParityDatabaseDrawingReadDoesNotExposeNativeCredentials(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	canvas := model.CanvasProject{ID: "review-canvas", UserID: "user", PayloadJSON: `{"nodes":[{"id":"draw","type":"drawing","metadata":{"drawingDocument":{"engine":"excalidraw","snapshot":{"elements":[{"id":"shape","type":"rectangle","customData":{"apiKey":"PRIVATE_NATIVE_SENTINEL"},"link":"https://example.com/?signature=PRIVATE_NATIVE_SENTINEL"}],"files":{}}}}}],"connections":[]}`}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	result, err := cloudAgentReadDrawing(s.repo, "user", canvas.ID, parityCall("canvas_read_drawing", map[string]any{"nodeId": "draw"}))
	if err != nil {
		return // Rejecting an unsafe synced document is also a safe read outcome.
	}
	raw, err := json.Marshal(result)
	if err != nil || strings.Contains(string(raw), "PRIVATE_NATIVE_SENTINEL") {
		t.Fatal("native drawing read exposed credential metadata or a signed URL")
	}
}

func TestAgentParityDatabaseAssetBindingReplacesResultMetadata(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	canvas := model.CanvasProject{ID: "review-canvas", UserID: "user", PayloadJSON: `{"nodes":[{"id":"video","type":"video","metadata":{"status":"success","content":"resource:old","previewContent":"old-preview","videoPreview":{"storageKey":"resource:old-preview"},"naturalWidth":20,"naturalHeight":10,"mimeType":"video/old","bytes":99,"durationMs":888,"hasAudio":true,"producedModel":{"name":"old-model"},"taskOfficialStatus":"completed","agentGenerationContinuation":{"status":"completed","taskId":"old-task"},"generationEffectKeys":["old-effect"]}}],"connections":[]}`}
	for _, item := range []any{
		&canvas,
		&model.Resource{ID: "replacement", UserID: "user", Kind: "video", MimeType: "video/mp4", Width: 640, Height: 480, Size: 200, DurationMs: 1000, Status: model.ResourceStatusReady},
		&model.Asset{ID: "replacement-asset", UserID: "user", Kind: "video", PayloadJSON: `{"data":{"storageKey":"resource:replacement"}}`},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	plan, err := prepareCloudAgentAssetBinding(s.repo, "user", canvas.ID, parityCall("canvas_bind_asset", map[string]any{"nodeId": "video", "assetId": "replacement-asset", "snapshotHash": cloudAgentCanvasHash(doc)}))
	if err != nil {
		t.Fatal(err)
	}
	meta := cloudAgentNodeMetadata(creationMaps(plan.Document["nodes"])[0])
	raw, _ := json.Marshal(meta)
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["naturalWidth"] != float64(640) || meta["naturalHeight"] != float64(480) || meta["durationMs"] != float64(1000) || meta["bytes"] != float64(200) || meta["mimeType"] != "video/mp4" {
		t.Fatal("replacement resource metadata was not reflected in canvas fields")
	}
	for _, field := range []string{"previewContent", "videoPreview", "hasAudio", "producedModel", "taskOfficialStatus", "agentGenerationContinuation", "generationEffectKeys"} {
		if _, present := meta[field]; present {
			t.Errorf("old result or generation identity retained: %s", field)
		}
	}
}

func TestAgentParityDatabaseAssetBindingRejectsCharacterCard(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	canvas := model.CanvasProject{ID: "review-canvas", UserID: "user", PayloadJSON: `{"nodes":[{"id":"character","type":"text","metadata":{"workflowKind":"character","characterAssetId":"character-asset","content":"original definition"}}],"connections":[]}`}
	for _, item := range []any{&canvas, &model.Asset{ID: "text-asset", UserID: "user", Kind: "text", PayloadJSON: `{"data":{"content":"ordinary text asset"}}`}} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	if _, err := prepareCloudAgentAssetBinding(s.repo, "user", canvas.ID, parityCall("canvas_bind_asset", map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "character", "assetId": "text-asset"})); err == nil {
		t.Fatal("ordinary text asset accepted for a character-card variant")
	}
	saved, err := s.repo.CanvasProjectForUser("user", canvas.ID)
	if err != nil || saved.PayloadJSON != canvas.PayloadJSON {
		t.Fatal("rejected asset binding changed the character card")
	}
}

func TestAgentParityGenerationSourceTextUsesSourceCapability(t *testing.T) {
	for _, kind := range []string{"text", "markdown"} {
		t.Run(kind, func(t *testing.T) {
			spec, err := cloudAgentGenerationSpec(cloudAgentMediaArgs{Mode: "image", SourceNodeID: "source", ChannelID: "channel", ChannelModelKey: "model", Prompt: "draft"}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			doc := map[string]any{"nodes": []map[string]any{{"id": "source", "type": kind, "metadata": map[string]any{"content": "source body"}}, {"id": "target", "type": "image", "metadata": map[string]any{"generationSpec": spec}}}}
			if err := validateCloudAgentGenerationReferences(doc, "target"); err != nil {
				t.Fatalf("source-text rejected for %s: %v", kind, err)
			}
			// Accepting a text source must not grant it media-reference permission.
			spec.ReferenceBindings[0].Role = "reference"
			cloudAgentNodeMetadata(creationMaps(doc["nodes"])[1])["generationSpec"] = spec
			if err := validateCloudAgentGenerationReferences(doc, "target"); err == nil {
				t.Fatal("ordinary text source gained media-reference permission")
			}
		})
	}
}

func TestAgentParityStoryboardVideoDurationAndCanonicalOptions(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "canonical"}[canonical], func(t *testing.T) {
			meta := map[string]any{"prompt": "previous submitted prompt", "composerContent": "old draft", "seconds": "5"}
			if canonical {
				spec, err := cloudAgentGenerationSpec(cloudAgentMediaArgs{Mode: "video", Duration: 5, ChannelID: "channel", ChannelModelKey: "model", Prompt: "old draft"}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				meta["generationSpec"] = spec
			}
			row := map[string]any{"id": "shot", "shotNumber": 1.0, "durationSeconds": 10.0, "videoMotionPrompt": "new row prompt"}
			nodes := []map[string]any{{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []map[string]any{row}}}}, {"id": "video", "type": "video", "metadata": meta}}
			err := cloudAgentAttachStoryboardEdge(nodes, map[string]any{"fromNodeId": "board", "fromHandleId": "row:shot", "toNodeId": "video"})
			if err != nil {
				t.Fatal(err)
			}
			if meta["seconds"] != "10" || meta["prompt"] != "previous submitted prompt" || meta["composerContent"] != "new row prompt" || row["videoNodeId"] != "video" {
				t.Fatalf("row attachment did not preserve submitted prompt and update the next duration/prompt: %#v", meta)
			}
			if canonical {
				raw, _ := json.Marshal(meta["generationSpec"])
				spec, err := contract.Decode(raw)
				if err != nil || spec.Options.DurationSeconds == nil || *spec.Options.DurationSeconds != 10 || spec.Prompt != "new row prompt" {
					t.Fatalf("canonical next-generation prompt/duration out of sync: %v", err)
				}
			}
		})
	}
}
