package commands

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"yingce/backend/internal/model"
)

// A persisted Agent scene must remain editable and renderable by the public
// command path, without turning its new authoring fields into arbitrary JSON.
func worldPrevisDocument(t *testing.T) Document {
	t.Helper()
	result, err := Plan(fixture(t, `{"id":"canvas","nodes":[],"connections":[]}`),
		command(Operation{Type: "create_previs_scene", ID: "scene", TemplateID: "empty"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	scene := PrevisScene(result.Document, "scene")
	authoring := fixture(t, `{
		"world":{"timeOfDay":10,"weather":"clear","ground":"grass","groundSize":1000,"horizon":true},
		"markers":[{"id":"a","label":"A","position":[0,0,0]}],
		"paths":[{"id":"route","label":"Route","points":[[0,0,0],[3,1,0],[6,0,0]]}],
		"objects":[
			{"id":"building","name":"Building","kind":"prop","propId":"building","variant":1,"params":{"floors":12,"style":"glass"},"setId":"set","color":"#aaaaaa","visible":true,"transform":{"position":[4,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"keyframes":[]},
			{"id":"garden","name":"Garden","kind":"layout","layout":{"template":"garden","seed":1,"size":110,"density":0.75,"crowd":true,"removed":[0,2]},"color":"#aaaaaa","visible":true,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"keyframes":[]},
			{"id":"actor","name":"Actor","kind":"actor","color":"#aaaaaa","visible":true,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},
			 "keyframes":[{"id":"k","time":0,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"easing":"linear","lift":1}],
			 "motionPath":{"points":[[0,0,0],[3,1,0],[6,0,0]],"speed":"walk","startDelay":0,"orientToPath":true,"metersPerSecond":2,"duration":4,"easing":"ease_in","curve":true,"speeds":[1,2],"holds":[0,1,0]},
			 "motions":[{"id":"nod","motion":"nod","start":0,"duration":1,"intensity":1}],
			 "travels":[{"id":"fly","style":"fly","start":1,"duration":2,"height":3}]}
		]
	}`)
	for key, value := range authoring {
		scene[key] = value
	}
	camera := maps(scene["cameras"])[0]
	camera["moves"] = fixture(t, `{"moves":[{"id":"follow","technique":"follow","start":0,"duration":4,"easing":"smooth","subjectId":"actor","params":{"side":"front","shotSize":"close_up","blend":1.5}}]}`)["moves"]
	scene["extension"] = map[string]any{"retain": true}
	return result.Document
}

func TestNativePrevisWorldStoredSceneEditAndRenderRoundTrip(t *testing.T) {
	doc := worldPrevisDocument(t)
	original := PrevisScene(doc, "scene")
	edit := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"scene","previsEntityOperations":[{"type":"settings","patch":{"title":"Edited"}}]}`)
	result, err := Plan(doc, command(edit), ownerContext())
	if err != nil {
		t.Fatalf("editing an existing world scene failed: %v", err)
	}
	scene := PrevisScene(result.Document, "scene")
	if scene["title"] != "Edited" || original["title"] == "Edited" {
		t.Fatal("edit did not preserve input isolation")
	}
	for _, field := range []string{"world", "markers", "paths", "objects", "cameras", "extension"} {
		before, _ := json.Marshal(original[field])
		after, _ := json.Marshal(scene[field])
		if string(before) != string(after) {
			t.Errorf("edit changed existing %s", field)
		}
	}
	raw, err := json.Marshal(result.Document)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := fixture(t, string(raw))
	shotID := text(maps(scene["shots"])[0]["id"])
	input, err := PreparePrevisRender(reloaded, "canvas", "scene", shotID, "render", nil)
	if err != nil {
		t.Fatalf("rendering a reloaded world scene failed: %v", err)
	}
	if len(input.Assets) != 0 || text(maps(input.Scene["objects"])[0]["propId"]) != "building" {
		t.Fatalf("procedural geometry incorrectly bound as external media: %+v", input)
	}
	next, outcome, err := ApplyPrevisResult(reloaded, input, "task",
		PrevisOutput{ResourceID: "video", PreviewResourceID: "preview", Width: 360, Height: 640, DurationMs: 5000, FrameCount: 120})
	if err != nil || outcome != "applied" {
		t.Fatalf("render result was not applied: %s %v", outcome, err)
	}
	if !reflect.DeepEqual(PrevisScene(next, "scene")["objects"], original["objects"]) {
		t.Fatal("render result discarded world authoring data")
	}
}

func TestNativePrevisWorldFieldsAreEditable(t *testing.T) {
	doc := worldPrevisDocument(t)
	scene := PrevisScene(doc, "scene")
	cameraID := text(maps(scene["cameras"])[0]["id"])
	changes := []map[string]any{
		{"type": "settings", "patch": map[string]any{"world": scene["world"], "markers": scene["markers"], "paths": scene["paths"]}},
		{"type": "upsert_camera", "id": cameraID, "patch": map[string]any{"moves": maps(scene["cameras"])[0]["moves"]}},
	}
	for _, object := range maps(scene["objects"]) {
		patch := map[string]any{}
		for key, value := range object {
			if key != "id" {
				patch[key] = value
			}
		}
		changes = append(changes, map[string]any{"type": "upsert_object", "id": object["id"], "patch": patch})
	}
	body, err := json.Marshal(map[string]any{"type": "edit_previs_scene", "id": "scene", "previsEntityOperations": changes})
	if err != nil {
		t.Fatal(err)
	}
	op := nativeConnectionOp(t, string(body))
	if _, err := Plan(doc, command(op), ownerContext()); err != nil {
		t.Fatalf("native authoring fields rejected: %v", err)
	}
}

func TestNativePrevisWorldRejectsMalformedStoredFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"world shape", func(s map[string]any) { s["world"] = "clear" }},
		{"weather enum", func(s map[string]any) { s["world"].(map[string]any)["weather"] = "acid" }},
		{"world field", func(s map[string]any) { s["world"].(map[string]any)["url"] = "https://evil.invalid/" }},
		{"horizon type", func(s map[string]any) { s["world"].(map[string]any)["horizon"] = "yes" }},
		{"markers shape", func(s map[string]any) { s["markers"] = "A" }},
		{"marker vector", func(s map[string]any) { maps(s["markers"])[0]["position"] = []any{0.0, 0.0} }},
		{"paths shape", func(s map[string]any) { s["paths"] = map[string]any{} }},
		{"path points", func(s map[string]any) { maps(s["paths"])[0]["points"] = []any{"point", "point"} }},
		{"unknown prop", func(s map[string]any) { maps(s["objects"])[0]["propId"] = "ufo" }},
		{"missing prop", func(s map[string]any) { delete(maps(s["objects"])[0], "propId") }},
		{"variant type", func(s map[string]any) { maps(s["objects"])[0]["variant"] = "1" }},
		{"variant fraction", func(s map[string]any) { maps(s["objects"])[0]["variant"] = 1.5 }},
		{"params shape", func(s map[string]any) { maps(s["objects"])[0]["params"] = []any{} }},
		{"params key", func(s map[string]any) { maps(s["objects"])[0]["params"] = map[string]any{"url": "file:///secret"} }},
		{"params range", func(s map[string]any) { maps(s["objects"])[0]["params"] = map[string]any{"floors": 81.0} }},
		{"params enum", func(s map[string]any) { maps(s["objects"])[0]["params"] = map[string]any{"style": "gothic"} }},
		{"layout shape", func(s map[string]any) { maps(s["objects"])[1]["layout"] = []any{} }},
		{"layout missing", func(s map[string]any) { delete(maps(s["objects"])[1], "layout") }},
		{"layout range", func(s map[string]any) { maps(s["objects"])[1]["layout"].(map[string]any)["size"] = 400.0 }},
		{"removed fraction", func(s map[string]any) { maps(s["objects"])[1]["layout"].(map[string]any)["removed"] = []any{0.5} }},
		{"motionPath shape", func(s map[string]any) { maps(s["objects"])[2]["motionPath"] = "route" }},
		{"motionPath speed", func(s map[string]any) { maps(s["objects"])[2]["motionPath"].(map[string]any)["speeds"] = []any{1.0} }},
		{"motionPath hold", func(s map[string]any) {
			maps(s["objects"])[2]["motionPath"].(map[string]any)["holds"] = []any{0.0, -1.0, 0.0}
		}},
		{"motionPath curve", func(s map[string]any) { maps(s["objects"])[2]["motionPath"].(map[string]any)["curve"] = "yes" }},
		{"motionPath mps", func(s map[string]any) { maps(s["objects"])[2]["motionPath"].(map[string]any)["metersPerSecond"] = 0.0 }},
		{"motionPath optional null", func(s map[string]any) { maps(s["objects"])[2]["motionPath"].(map[string]any)["duration"] = nil }},
		{"keyframe lift", func(s map[string]any) { maps(maps(s["objects"])[2]["keyframes"])[0]["lift"] = -1.0 }},
		{"travel enum", func(s map[string]any) { maps(maps(s["objects"])[2]["travels"])[0]["style"] = "warp" }},
		{"travel height", func(s map[string]any) { maps(maps(s["objects"])[2]["travels"])[0]["height"] = "3" }},
		{"motion enum", func(s map[string]any) { maps(maps(s["objects"])[2]["motions"])[0]["motion"] = "warp" }},
		{"moves shape", func(s map[string]any) { maps(s["cameras"])[0]["moves"] = "follow" }},
		{"moves technique", func(s map[string]any) { maps(maps(s["cameras"])[0]["moves"])[0]["technique"] = "warp" }},
		{"moves subject", func(s map[string]any) { delete(maps(maps(s["cameras"])[0]["moves"])[0], "subjectId") }},
		{"moves params key", func(s map[string]any) {
			maps(maps(s["cameras"])[0]["moves"])[0]["params"] = map[string]any{"url": "https://evil.invalid/"}
		}},
		{"moves params enum", func(s map[string]any) {
			maps(maps(s["cameras"])[0]["moves"])[0]["params"] = map[string]any{"side": "diagonal"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := worldPrevisDocument(t)
			tc.mutate(PrevisScene(doc, "scene"))
			edit := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"scene","previsEntityOperations":[{"type":"settings","patch":{"title":"Edited"}}]}`)
			if _, err := Plan(doc, command(edit), ownerContext()); err == nil {
				t.Error("malformed stored data accepted by scene edit")
			}
			shotID := text(maps(PrevisScene(doc, "scene")["shots"])[0]["id"])
			if _, err := PreparePrevisRender(doc, "canvas", "scene", shotID, "render", nil); err == nil {
				t.Error("malformed stored data accepted by render")
			}
		})
	}
}

func TestNativePrevisWorldDoesNotBypassAuthorizationOrFieldAllowlist(t *testing.T) {
	doc := worldPrevisDocument(t)
	edit := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"scene","previsEntityOperations":[{"type":"settings","patch":{"title":"Edited"}}]}`)
	ctx := ownerContext()
	ctx.Principal.Scopes = nil
	if _, err := Plan(doc, command(edit), ctx); err == nil {
		t.Fatal("principal without write scope edited world scene")
	}
	for _, patch := range []string{
		`{"url":"file:///secret.glb"}`, `{"assetId":"foreign"}`,
		`{"taskStatus":"succeeded"}`, `{"params":{"storageKey":"resource:foreign"}}`,
	} {
		op := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"scene","previsEntityOperations":[{"type":"upsert_object","id":"building","patch":`+patch+`}]}`)
		if _, err := Plan(doc, command(op), ownerContext()); err == nil {
			t.Errorf("unsafe field accepted: %s", patch)
		}
	}
}

func TestNativePrevisWorldPersistsExistingSceneEdit(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	doc := worldPrevisDocument(t)
	doc["id"] = before.CanvasID
	doc["referenceFieldsVersion"] = float64(1)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", before.CanvasID).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	op := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"scene","previsEntityOperations":[{"type":"settings","patch":{"title":"Persisted world edit"}}]}`)
	req := command(op)
	req.CanvasID, req.DocumentEpoch, req.BaseRevision = before.CanvasID, before.DocumentEpoch, before.Revision
	req.OperationID = "world-edit"
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, req); err != nil {
		t.Fatalf("existing world scene edit failed to commit: %v", err)
	}
	stored, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	scene := PrevisScene(stored.Document, "scene")
	if scene["title"] != "Persisted world edit" || stored.Revision != before.Revision+1 {
		t.Fatal("world edit not durably projected", stored.Revision, scene["title"])
	}
	if !reflect.DeepEqual(scene["objects"], PrevisScene(doc, "scene")["objects"]) {
		t.Fatal("persisted edit lost existing world objects")
	}
}
