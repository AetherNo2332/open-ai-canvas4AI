package assets

import "testing"

func TestAgentParityNativeDrawingResourcesRemainReferenced(t *testing.T) {
	raw := `{"nodes":[{"id":"d","metadata":{"drawingDocument":{"snapshot":{"files":{"f":{"dataURL":"resource:excal-image"}},"document":{"store":{"asset:a":{"props":{"src":"resource:tl-image"}}}}}},"label":"resource:prose","other":{"src":"resource:unrelated"}}}]}`
	refs, err := CollectDocumentResourceReferences(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("native drawings must retain exactly their two platform files: %#v", refs)
	}
	for _, ref := range refs {
		if ref.NodeID != "d" {
			t.Fatal("drawing owner lost")
		}
	}
}

func TestCollectDocumentResourceReferencesIncludesNestedVideoPreview(t *testing.T) {
	raw := `{"nodes":[{"id":"node-video","metadata":{"videoPreview":{"storageKey":"resource:poster-1","source":{"id":"nested-not-node","url":"/api/resources/poster-2/file"}}}}]}`
	refs, err := CollectDocumentResourceReferences(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DocumentResourceReference{}
	for _, ref := range refs {
		got[ref.ResourceID] = ref
	}
	if ref := got["poster-1"]; ref.Path != "nodes[node-video].metadata.videoPreview.storageKey" || ref.NodeID != "node-video" || ref.ReferenceType != "storageKey" {
		t.Fatalf("nested storageKey reference = %#v", ref)
	}
	if ref := got["poster-2"]; ref.Path != "nodes[node-video].metadata.videoPreview.source.url" || ref.NodeID != "node-video" {
		t.Fatalf("nested URL reference = %#v", ref)
	}
}

func TestCollectDocumentResourceReferencesUsesExplicitFieldsOnly(t *testing.T) {
	raw := `{"nodes":[{"id":"node-1","metadata":{"label":"resource:not-a-reference","videoPreview":{"resourceId":"poster-3"}}}],"log":"/api/resources/log-1/file"}`
	refs, err := CollectDocumentResourceReferences(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ResourceID != "poster-3" || refs[0].NodeID != "node-1" || refs[0].ReferenceType != "resourceId" {
		t.Fatalf("references = %#v", refs)
	}
}

func TestCollectDocumentResourceReferencesReportsInvalidJSON(t *testing.T) {
	if _, err := CollectDocumentResourceReferences(`{"nodes":`); err == nil {
		t.Fatal("expected malformed document to be rejected")
	}
}
