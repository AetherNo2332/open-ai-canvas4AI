package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
)

type imageToolSubmitter struct {
	stubSubmitter
	requests []TaskRequest
}

func (s *imageToolSubmitter) Prepare(ctx context.Context, p identity.Principal, req TaskRequest) (PreparedTask, error) {
	s.requests = append(s.requests, req)
	prepared, err := s.stubSubmitter.Prepare(ctx, p, req)
	if err != nil {
		return prepared, err
	}
	raw, err := json.Marshal(req.Input)
	prepared.Task.InputJSON = string(raw)
	return prepared, err
}

// Reuse the actual image-tool hook output and task payload; only provider
// execution is replaced, so this covers ownership, persistence and admission.
func TestImageToolTaskAcceptance(t *testing.T) {
	path := os.Getenv("CANVAS_NODE_ACCEPTANCE_FILE")
	if path == "" {
		t.Skip("run the Canvas node acceptance CI job to generate producer operations")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Results []struct {
			Name     string
			SetupOps []commands.Operation
			Ops      []commands.Operation
		}
		ImageEdits []struct {
			Edit, NodeID, SourceNodeID, ExpectedPrompt string
			Requests                                   []TaskRequest
		}
	}
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.ImageEdits) != 2 {
		t.Fatal("angle or lighting task case was omitted")
	}
	for _, edit := range report.ImageEdits {
		t.Run(edit.Edit, func(t *testing.T) {
			_, db, repo, cs, p, base, _ := taskFixture(t)
			ctx := context.Background()
			if err := db.Create(&model.Resource{ID: "local", UserID: p.OwnerID, Kind: "image", MimeType: "image/png", Status: model.ResourceStatusReady}).Error; err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range report.Results {
				if c.Name != "image:"+edit.Edit+"-create" {
					continue
				}
				found = true
				for i, ops := range [][]commands.Operation{c.SetupOps, c.Ops} {
					snap, err := cs.ReadSnapshot(ctx, p, base.Command.CanvasID)
					if err != nil {
						t.Fatal(err)
					}
					req := commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: fmt.Sprintf("image-tool-%d", i), BaseRevision: snap.Revision, ExpectedEntityVersions: snap.EntityVersions, Operations: ops}
					if _, err = cs.Execute(ctx, p, req); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !found || len(edit.Requests) == 0 {
				t.Fatal("image tool operations or submission missing")
			}
			cs = commands.NewService(repo, commands.ServiceOptions{})
			snap, err := cs.ReadSnapshot(ctx, p, base.Command.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			task := edit.Requests[len(edit.Requests)-1]
			task.Mode, _ = task.Input["mode"].(string)
			if task.Prompt != edit.ExpectedPrompt || task.Input["prompt"] != edit.ExpectedPrompt {
				t.Fatal("generated prompt changed before task admission")
			}
			submitter := &imageToolSubmitter{}
			svc := NewService(repo, cs, submitter)
			req := SubmitRequest{Command: commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "image-tool-submit", BaseRevision: snap.Revision, ExpectedEntityVersions: snap.EntityVersions}, TargetNodeID: edit.NodeID, Task: task}
			first, err := svc.Submit(ctx, p, req)
			if err != nil {
				t.Fatal(err)
			}
			repeat, err := svc.Submit(ctx, p, req)
			if err != nil || !reflect.DeepEqual(first, repeat) || len(submitter.requests) != 1 {
				t.Fatalf("task replay was not idempotent: %v", err)
			}
			var saved model.Task
			if err = db.First(&saved, "id = ?", first.TaskID).Error; err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if err = json.Unmarshal([]byte(saved.InputJSON), &input); err != nil {
				t.Fatal(err)
			}
			refs, _ := input["referenceImages"].([]any)
			if saved.Prompt != edit.ExpectedPrompt || input["prompt"] != edit.ExpectedPrompt || len(refs) != 1 || refs[0].(map[string]any)["storageKey"] != "resource:local" {
				t.Fatalf("admitted task lost the prompt or original image: %#v", input)
			}
		})
	}
}
