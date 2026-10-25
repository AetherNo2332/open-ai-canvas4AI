package commands

import "testing"

func TestEditorUploadResetRoundTrip(t *testing.T) {
	for _, kind := range []string{"image", "video", "audio", "text"} {
		t.Run(kind, func(t *testing.T) {
			doc := fixture(t, `{"nodes":[{"id":"upload","type":"`+kind+`","metadata":{"content":"old","composerContent":"old","prompt":"old","model":"old","count":2,"size":"16:9","storageKey":"resource:old","copiedFromNodeId":"source","versionOfNodeId":"source"}}],"connections":[]}`)
			ctx := ownerContext()
			resourceKind, mime := kind, kind+"/example"
			if kind == "text" {
				resourceKind, mime = "file", "text/plain"
			}
			ctx.Resources = map[string]ResourceMetadata{"owned": {ID: "owned", Kind: resourceKind, MimeType: mime}}
			ctx.ResourceAllowed = func(id string) bool { return id == "owned" }
			patch := map[string]any{"content": nil, "model": nil, "count": nil, "size": nil}
			if kind == "text" {
				patch["content"], patch["richText"] = "new body", nil
			}
			result, err := Plan(doc, command(Operation{Type: "bind_resource", ID: "upload", ResourceID: "owned"}, Operation{Type: "update_node", ID: "upload", Patch: patch}), ctx)
			if err != nil {
				t.Fatal(err)
			}
			meta := entity(result.Document, "node:upload")["metadata"].(map[string]any)
			for _, key := range []string{"model", "count", "size", "copiedFromNodeId", "versionOfNodeId"} {
				if _, exists := meta[key]; exists {
					t.Fatalf("retained %s: %#v", key, meta)
				}
			}
			if kind != "text" && meta["content"] != "/api/resources/owned/file" {
				t.Fatal("media URL lost", meta)
			}
			if kind == "text" && meta["content"] != "new body" {
				t.Fatal("text body lost", meta)
			}
		})
	}
}
