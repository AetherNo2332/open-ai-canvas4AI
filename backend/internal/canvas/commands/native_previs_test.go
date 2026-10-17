package commands

import "testing"

func TestNativePrevisPreservesRigOpticsAndRejectsBrokenReferences(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[],"connections":[]}`)
	created, err := Plan(d, command(Operation{Type: "create_previs_scene", ID: "scene", TemplateID: "empty"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	scene := maps(created.Document["previsScenes"])[0]
	cameraID := text(maps(scene["cameras"])[0]["id"])
	scene["extension"] = "retained"
	op := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"scene","previsEntityOperations":[{"type":"upsert_camera","id":"`+cameraID+`","patch":{"aperture":2.8,"focusDistance":6,"fov":48,"near":0.1,"far":800}},{"type":"upsert_object","id":"actor","patch":{"name":"Actor","kind":"actor","color":"#aaaaaa","visible":true,"castShadow":true,"receiveShadow":true,"transform":{"position":[0,0,0],"rotation":[0,0,0],"scale":[1,1,1]},"pose":"walk","keyframes":[],"rig":{"status":"ready","boneMap":{"hips":"Hips"},"animationNames":["Walk"]},"motionClips":[{"id":"walk","name":"Walk","sourceAnimation":"Walk","start":0,"duration":2,"playbackRate":1,"loop":true}],"activeMotionClipId":"walk","boneTracks":[{"bone":"hips","keyframes":[{"id":"key","time":0,"rotation":[0,0,0,1]}]}]}}]}`)
	result, err := Plan(created.Document, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	next := maps(result.Document["previsScenes"])[0]
	if next["extension"] != "retained" || maps(next["cameras"])[0]["aperture"] != 2.8 || len(maps(next["objects"])[0]["boneTracks"].([]any)) != 1 {
		t.Fatal("native editor state lost", next)
	}
	for _, patch := range []string{`{"sourceNodeId":"missing"}`, `{"url":"file:///C:/secret.glb"}`, `{"assetId":"foreign"}`, `{"boneOverrides":{"hips":[0,0,0,0]}}`, `{"taskStatus":"succeeded"}`, `{"activeMotionClipId":"missing"}`} {
		bad := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"scene","previsEntityOperations":[{"type":"upsert_object","id":"actor","patch":`+patch+`}]}`)
		if _, err := Plan(result.Document, command(bad), ownerContext()); err == nil {
			t.Fatal("invalid native state accepted", patch)
		}
	}
}

func TestNativePrevisInitializesExactEditorIDsAtomically(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[],"connections":[]}`)
	op := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"new-scene","action":"initialize","previsEntityOperations":[{"type":"settings","patch":{"title":"Native scene","activeShotId":"shot"}},{"type":"upsert_camera","id":"camera","patch":{"name":"Camera","transform":{"position":[0,2,5],"rotation":[0,0,0],"scale":[1,1,1]},"target":[0,0,0],"focalLength":35,"fov":50,"aperture":2.8,"focusDistance":5,"near":0.1,"far":1000,"keyframes":[]}},{"type":"upsert_shot","id":"shot","patch":{"name":"Shot","cameraId":"camera","duration":3,"fps":24,"shotSize":"wide","cameraMove":"static","prompt":"","aspectRatio":"16:9"}}]}`)
	result, err := Plan(d, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if maps(result.Document["previsScenes"])[0]["activeShotId"] != "shot" {
		t.Fatal("editor IDs were replaced")
	}
	if _, err := Plan(result.Document, command(op), ownerContext()); err == nil {
		t.Fatal("scene reinitialization accepted")
	}
	bad := nativeConnectionOp(t, `{"type":"edit_previs_scene","id":"new-scene","previsEntityOperations":[{"type":"delete_camera","id":"camera"}]}`)
	if _, err := Plan(result.Document, command(bad), ownerContext()); err == nil {
		t.Fatal("camera referenced by shot deleted")
	}
}
