package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// CI supplies the output of the real TypeScript producers, not hand-written Go
// equivalents. Reopening the service ensures reads come from SQLite persistence.
func TestNodeFunctionAcceptance(t *testing.T) {
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
			Name       string
			Status     string
			Error      string
			SetupError string
			SetupOps   []Operation
			Ops        []Operation
			After      Document
		}
	}
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) < 100 {
		t.Fatal("node feature cases were omitted")
	}
	svc, db, _, _ := commandFixture(t)
	if err = db.Create(&model.Resource{ID: "local", UserID: "owner", Kind: "image", MimeType: "image/png", Status: model.ResourceStatusReady}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, p := context.Background(), ownerContext().Principal
	covered := map[string]bool{}
	for index, c := range report.Results {
		t.Run(c.Name, func(t *testing.T) {
			covered[c.Name] = true
			if c.Status == "guarded-no-change" {
				if len(c.Ops) != 0 {
					t.Fatal("guard modified document")
				}
				return
			}
			if c.Status != "converted" || c.SetupError != "" {
				t.Fatalf("producer failed: %s %s", c.Error, c.SetupError)
			}
			snap, err := svc.Create(ctx, p, CreateCanvasRequest{ClientID: "feature-ci", OperationID: fmt.Sprintf("create-%d", index), Title: c.Name})
			if err != nil {
				t.Fatal(err)
			}
			apply := func(ops []Operation, phase string) {
				t.Helper()
				if len(ops) == 0 {
					return
				}
				for i := range ops {
					if ops[i].ID == "audit" {
						ops[i].ID = snap.CanvasID
					}
				}
				req := Command{Protocol: ProtocolVersion, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "feature-ci", OperationID: fmt.Sprintf("%s-%d", phase, index), BaseRevision: snap.Revision, ExpectedEntityVersions: snap.EntityVersions, Operations: ops}
				first, err := svc.Execute(ctx, p, req)
				if err != nil {
					t.Fatalf("%s: %v", phase, err)
				}
				repeat, err := svc.Execute(ctx, p, req)
				if err != nil || !reflect.DeepEqual(first, repeat) {
					t.Fatalf("%s replay changed receipt: %v", phase, err)
				}
				snap, err = NewService(repository.New(db), ServiceOptions{}).ReadSnapshot(ctx, p, snap.CanvasID)
				if err != nil {
					t.Fatal(err)
				}
			}
			apply(c.SetupOps, "setup")
			apply(c.Ops, "edit")
			wantNodes, gotNodes := maps(c.After["nodes"]), maps(snap.Document["nodes"])
			if len(wantNodes) != len(gotNodes) {
				t.Fatalf("node count: got %d want %d", len(gotNodes), len(wantNodes))
			}
			for _, want := range wantNodes {
				got := entity(snap.Document, "node:"+text(want["id"]))
				if got == nil || got["type"] != want["type"] {
					t.Fatalf("missing node %v", want["id"])
				}
			}
			for _, op := range c.Ops {
				if op.Type != "update_node" {
					continue
				}
				node := entity(snap.Document, "node:"+op.ID)
				d, ok := capability.BuiltinRegistry().Resolve(text(node["type"]))
				if !ok {
					t.Fatal("unknown persisted type")
				}
				for key, want := range op.Patch {
					field, ok := d.PatchFields[key]
					if !ok {
						t.Fatalf("unknown patch %s", key)
					}
					var value any = node
					for _, part := range strings.Split(field.Path, ".") {
						object, _ := value.(map[string]any)
						value = object[part]
					}
					// An empty model selection is canonically absent after generation
					// contract mirroring; it does not select an empty provider model.
					if key == "model" && want == "" && value == nil {
						continue
					}
					a, _ := json.Marshal(value)
					b, _ := json.Marshal(want)
					if string(a) != string(b) {
						t.Fatalf("%s lost: got %s want %s", key, a, b)
					}
				}
			}
		})
	}
	for _, raw := range Capabilities()["nodes"].([]any) {
		node := raw.(map[string]any)
		if node["baseType"] != nil {
			continue
		}
		for _, action := range []string{"create", "rename-move-resize-lock", "clipboard-paste", "delete"} {
			if !covered[text(node["type"])+":"+action] {
				t.Errorf("missing %s:%s", node["type"], action)
			}
		}
	}
}
