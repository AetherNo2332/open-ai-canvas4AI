package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestGenerationReferenceCharacterDraft(t *testing.T) {
	for _, mode := range []string{"image", "video"} {
		for _, source := range []string{"image", "character"} {
			for _, edit := range []string{"new", "prompt", "options"} {
				t.Run(mode+"/"+source+"/"+edit, func(t *testing.T) {
					doc := fixture(t, fmt.Sprintf(`{"referenceFieldsVersion":1,"nodes":[{"id":"hero","type":"image","metadata":{}},{"id":"target","type":"%s","metadata":{}}],"connections":[]}`, mode))
					if source == "character" {
						n := maps(doc["nodes"])[0]
						n["type"] = "text"
						n["metadata"] = map[string]any{"workflowKind": "character", "characterAssetId": "asset"}
					}
					spec := fixture(t, fmt.Sprintf(`{"version":1,"mode":"%s","prompt":"before","options":{},"referenceBindings":[{"id":"hero-ref","nodeId":"hero","mediaType":"image","role":"reference","order":0,"resolution":"latest"}],"textInputMode":"prompt-only"}`, mode))
					if edit != "new" {
						raw, _ := json.Marshal(spec)
						var previous map[string]any
						json.Unmarshal(raw, &previous)
						maps(doc["nodes"])[1]["metadata"].(map[string]any)["generationSpec"] = previous
					}
					if edit == "prompt" {
						spec["prompt"] = "after"
					}
					if edit == "options" {
						spec["options"] = map[string]any{"count": float64(1)}
						if mode == "video" {
							spec["options"] = map[string]any{"durationSeconds": float64(5)}
						}
					}
					before, _ := json.Marshal(doc)
					result, err := Plan(doc, command(Operation{Type: "update_node", ID: "target", Patch: map[string]any{"generationSpec": map[string]any(spec)}}), ownerContext())
					if err != nil {
						t.Fatalf("valid %s reference rejected: %v", source, err)
					}
					after, _ := json.Marshal(doc)
					if string(before) != string(after) {
						t.Fatal("planner changed its input")
					}
					got := maps(result.Document["nodes"])[1]["metadata"].(map[string]any)["generationSpec"].(map[string]any)
					if len(maps(got["referenceBindings"])) != 1 || got["prompt"] != spec["prompt"] {
						t.Fatalf("reference or edited prompt lost: %#v", got)
					}
				})
			}
		}
	}
}

func TestGenerationReferenceCharacterPersistsAndReloadsWithReceipt(t *testing.T) {
	for _, mode := range []string{"image", "video"} {
		t.Run(mode, func(t *testing.T) {
			svc, db, _, _ := commandFixture(t)
			canvasID := "character-save"
			payload := fmt.Sprintf(`{"id":"character-save","referenceFieldsVersion":1,"nodes":[{"id":"hero","type":"text","title":"角色","metadata":{"workflowKind":"character","characterAssetId":"asset"}},{"id":"target","type":"%s","metadata":{}}],"connections":[]}`, mode)
			if err := db.Create(&model.CanvasProject{ID: canvasID, UserID: "owner", Title: "引用保存", Revision: 1, PayloadJSON: payload}).Error; err != nil {
				t.Fatal(err)
			}
			current, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, canvasID)
			if err != nil {
				t.Fatal(err)
			}
			for _, prompt := range []string{"first save", "edited prompt"} {
				spec := fixture(t, fmt.Sprintf(`{"version":1,"mode":"%s","prompt":"%s","options":{},"referenceBindings":[{"id":"hero-ref","nodeId":"hero","mediaType":"image","role":"reference","order":0,"resolution":"latest"}],"textInputMode":"prompt-only"}`, mode, prompt))
				req := mergeRequest(current, prompt, Operation{Type: "update_node", ID: "target", Patch: map[string]any{"generationSpec": map[string]any(spec)}})
				// Request identities must use printable identifiers, not prompt text.
				req.OperationID = fmt.Sprintf("save-%d", current.Revision)
				receipt, err := svc.Execute(context.Background(), ownerContext().Principal, req)
				if err != nil {
					t.Fatalf("persistent %s save rejected: %v", mode, err)
				}
				reopened := NewService(repository.New(db), ServiceOptions{})
				current, err = reopened.ReadSnapshot(context.Background(), ownerContext().Principal, canvasID)
				if err != nil {
					t.Fatal(err)
				}
				target := entity(current.Document, "node:target")
				got := target["metadata"].(map[string]any)["generationSpec"].(map[string]any)
				if got["prompt"] != prompt || len(maps(got["referenceBindings"])) != 1 || len(maps(current.Document["connections"])) != 1 {
					t.Fatalf("reopened state lost reference, edge, or edit: %#v", current.Document)
				}
				recovered, err := reopened.GetReceipt(context.Background(), ownerContext().Principal, canvasID, current.DocumentEpoch, req.ClientID, req.OperationID)
				if err != nil || recovered.OperationID != receipt.OperationID {
					t.Fatalf("saved command cannot recover its receipt: %v", err)
				}
			}
		})
	}
}

func TestGenerationReferenceRejectsInvalidSource(t *testing.T) {
	for _, tc := range []struct{ name, nodeType, nodeID, media, role string }{
		{"missing", "image", "missing", "image", "reference"},
		{"self", "image", "target", "image", "reference"},
		{"wrong-media", "video", "source", "image", "reference"},
		{"text-as-image", "text", "source", "image", "reference"},
		{"text-as-media-reference", "text", "source", "text", "reference"},
		{"video-as-first-frame", "video", "source", "video", "first-frame"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := fixture(t, fmt.Sprintf(`{"nodes":[{"id":"source","type":"%s"},{"id":"target","type":"image","metadata":{}}],"connections":[]}`, tc.nodeType))
			spec := fixture(t, fmt.Sprintf(`{"version":1,"mode":"image","prompt":"","options":{},"referenceBindings":[{"id":"ref","nodeId":"%s","mediaType":"%s","role":"%s","order":0,"resolution":"latest"}],"textInputMode":"prompt-only"}`, tc.nodeID, tc.media, tc.role))
			before, _ := json.Marshal(doc)
			_, err := Plan(doc, command(Operation{Type: "update_node", ID: "target", Patch: map[string]any{"generationSpec": map[string]any(spec)}}), ownerContext())
			after, _ := json.Marshal(doc)
			if err == nil || string(before) != string(after) {
				t.Fatalf("invalid reference accepted or document changed: %v", err)
			}
		})
	}
}
