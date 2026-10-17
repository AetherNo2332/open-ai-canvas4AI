package commands

import (
	"encoding/json"
	"testing"
)

func TestPrevisFreezeAndResultSourceGuard(t *testing.T) {
	result, err := Plan(fixture(t, `{"id":"canvas","nodes":[],"connections":[]}`), command(Operation{Type: "create_previs_scene", ID: "scene1", TemplateID: "empty"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	scenes := maps(result.Document["previsScenes"])
	shots := maps(scenes[0]["shots"])
	shotID := text(shots[0]["id"])
	before, _ := json.Marshal(result.Document)
	input, err := PreparePrevisRender(result.Document, "canvas", "scene1", shotID, "op", nil)
	if err != nil {
		t.Fatal(err)
	}
	if input.SourceHash == "" || input.ShotID != shotID || input.Width != 360 || input.Height != 640 {
		t.Fatalf("bad frozen input: %+v", input)
	}
	after, _ := json.Marshal(result.Document)
	if string(before) != string(after) {
		t.Fatal("preparation mutated input")
	}
	output := PrevisOutput{ResourceID: "video", PreviewResourceID: "preview", Width: 360, Height: 640, DurationMs: 2000, FrameCount: 48}
	next, outcome, err := ApplyPrevisResult(result.Document, input, "task", output)
	if err != nil || outcome != "applied" || len(maps(next["nodes"])) != 2 {
		t.Fatalf("not materialized: %v %s %+v", err, outcome, next)
	}
	shot := maps(maps(next["previsScenes"])[0]["shots"])[0]
	if text(shot["clayVideoNodeId"]) == "" || text(shot["previewNodeId"]) == "" {
		t.Fatal("shot binding absent")
	}
	shots[0]["duration"] = 9.0
	next, outcome, err = ApplyPrevisResult(result.Document, input, "task", output)
	if err != nil || outcome != "source_changed" || len(maps(next["nodes"])) != 0 {
		t.Fatal("changed shot was overwritten")
	}
}
