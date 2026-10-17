package commands

import (
	"encoding/json"
	"testing"
)

func TestTimelineNativeEditAndReferenceValidation(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[{"id":"video","type":"video"}],"connections":[]}`)
	op := nativeConnectionOp(t, `{"type":"edit_timeline","id":"canvas","timelineOperations":[{"type":"upsert_track","id":"track","patch":{"kind":"video","label":"视频","order":0}},{"type":"upsert_clip","id":"clip","patch":{"kind":"video","nodeId":"video","trackId":"track","startMs":0,"durationMs":1200,"volume":1}}]}`)
	r, err := Plan(d, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	timeline := r.Document["timeline"].(map[string]any)
	if timeline["durationMs"] != 1200.0 || !r.ReadSet["node:video"] || !r.ReadSet["project:timeline"] {
		t.Fatalf("timeline/read set: %v %v", timeline, r.ReadSet)
	}
	removedNode, err := Plan(r.Document, command(Operation{Type: "delete_node", ID: "video"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(maps(removedNode.Document["timeline"].(map[string]any)["clips"])) != 0 {
		t.Fatal("node deletion left timeline dangling")
	}
	maps(timeline["clips"])[0]["extension"] = true
	update := nativeConnectionOp(t, `{"type":"edit_timeline","id":"canvas","timelineOperations":[{"type":"upsert_clip","id":"clip","patch":{"startMs":2000}}]}`)
	next, err := Plan(r.Document, command(update), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	changed := next.Document["timeline"].(map[string]any)
	if changed["durationMs"] != 3200.0 || maps(changed["clips"])[0]["extension"] != true {
		t.Fatal("timeline extension or end lost")
	}
	bad := nativeConnectionOp(t, `{"type":"edit_timeline","id":"canvas","timelineOperations":[{"type":"upsert_clip","id":"clip","patch":{"nodeId":"missing"}}]}`)
	original, _ := json.Marshal(r.Document)
	if _, err := Plan(r.Document, command(bad), ownerContext()); err == nil {
		t.Fatal("dangling timeline node accepted")
	}
	after, _ := json.Marshal(r.Document)
	if string(original) != string(after) {
		t.Fatal("failed timeline edit changed input")
	}
	del := nativeConnectionOp(t, `{"type":"edit_timeline","id":"canvas","timelineOperations":[{"type":"delete_track","id":"track"}]}`)
	removed, err := Plan(r.Document, command(del), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(maps(removed.Document["timeline"].(map[string]any)["clips"])) != 0 {
		t.Fatal("deleted track left orphan clips")
	}
}
func TestTimelineDirectMediaMustUseOwnedResource(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[],"connections":[]}`)
	for _, locator := range []string{"file:///C:/private/video.mp4", "http://127.0.0.1/private", "resource:private"} {
		op := nativeConnectionOp(t, `{"type":"edit_timeline","id":"canvas","timelineOperations":[{"type":"upsert_track","id":"track","patch":{"kind":"video","label":"视频","order":0}},{"type":"upsert_clip","id":"clip","patch":{"kind":"video","nodeId":"direct:x","trackId":"track","startMs":0,"durationMs":1200,"directMedia":{"id":"x","kind":"video","title":"视频","storageKey":"`+locator+`"}}}]}`)
		if _, err := Plan(d, command(op), ownerContext()); err == nil {
			t.Fatal("unowned direct media accepted", locator)
		}
	}
}
func TestTimelineAssetIdentityCannotBeForged(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[],"connections":[]}`)
	op := nativeConnectionOp(t, `{"type":"edit_timeline","id":"canvas","timelineOperations":[{"type":"upsert_track","id":"track","patch":{"kind":"video","label":"视频","order":0}},{"type":"upsert_clip","id":"clip","patch":{"kind":"video","nodeId":"direct:x","trackId":"track","startMs":0,"durationMs":1200,"directMedia":{"id":"x","assetId":"foreign","kind":"video","title":"视频","storageKey":"resource:owned"}}}]}`)
	ctx := ownerContext()
	ctx.ResourceAllowed = func(string) bool { return true }
	if _, err := Plan(d, command(op), ctx); err == nil {
		t.Fatal("foreign asset identity accepted with owned bytes")
	}
}
