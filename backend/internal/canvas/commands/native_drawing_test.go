package commands

import "testing"

func TestNativeDrawingInitializationAndOrdering(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"drawing","type":"drawing","metadata":{"drawingEngine":"tldraw"}}],"connections":[]}`)
	op := nativeConnectionOp(t, `{"type":"edit_drawing","id":"drawing","engine":"tldraw","drawingId":"native-document","drawingSnapshot":{"document":{"store":{},"schema":{"schemaVersion":2}}},"drawingOperations":[{"type":"upsert","id":"document:document","record":{"id":"document:document","typeName":"document","gridSize":10,"name":"Document","meta":{}}},{"type":"upsert","id":"page:one","record":{"id":"page:one","typeName":"page","name":"Page","index":"a1","meta":{}}}]}`)
	r, err := Plan(d, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	meta := entity(r.Document, "node:drawing")["metadata"].(map[string]any)
	if meta["drawingId"] != "native-document" || meta["drawingDocument"].(map[string]any)["pageCount"] != 1 {
		t.Fatal("native document not initialized")
	}
	if _, err := Plan(r.Document, command(op), ownerContext()); err == nil {
		t.Fatal("reinitialization replaced existing native document")
	}
	ex := fixture(t, `{"nodes":[{"id":"drawing","type":"drawing","metadata":{"drawingEngine":"excalidraw"}}],"connections":[]}`)
	order := nativeConnectionOp(t, `{"type":"edit_drawing","id":"drawing","engine":"excalidraw","drawingOperations":[{"type":"upsert","id":"z","record":{"id":"z","type":"rectangle","x":0,"y":0,"width":20,"height":20}},{"type":"upsert","id":"a","record":{"id":"a","type":"rectangle","x":0,"y":0,"width":20,"height":20}}],"drawingElementIds":["z","a"]}`)
	r, err = Plan(ex, command(order), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	elements := maps(entity(r.Document, "node:drawing")["metadata"].(map[string]any)["drawingDocument"].(map[string]any)["snapshot"].(map[string]any)["elements"])
	if text(elements[0]["id"]) != "z" {
		t.Fatal("native drawing layer order lost")
	}
}
