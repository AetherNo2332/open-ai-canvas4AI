package mediaversions

import (
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"testing"
)

func TestLegacyFamilyImportsOwnedReadySiblingsWithoutRewiring(t *testing.T) {
	repo, scope, node := versionFixture(t, "image")
	metadata(node)["versionOfNodeId"], metadata(node)["versionLabel"] = "family", "B"
	sibling := map[string]any{"id": "sibling", "type": "image", "metadata": map[string]any{"storageKey": "resource:new", "content": "/api/resources/new/file", "status": "success", "versionOfNodeId": "family", "versionLabel": "A", "composerContent": "untouched"}}
	missing := map[string]any{"id": "missing", "type": "image", "metadata": map[string]any{"storageKey": "resource:unavailable", "versionOfNodeId": "family", "versionLabel": "C"}}
	foreignFamily := map[string]any{"id": "other", "type": "image", "metadata": map[string]any{"storageKey": "resource:new", "versionOfNodeId": "other-family"}}
	frozen := encode(sibling)
	versionTx(t, repo, func(tx *repository.Repository) error {
		_, err := Begin(tx, scope, node, model.Task{ID: "new-attempt", UserID: "owner", Status: model.TaskStatusQueued}, "new_version", []map[string]any{node, missing, foreignFamily, sibling})
		return err
	})
	versions, err := repo.CanvasMediaVersions(scope, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("legacy import count: %+v", versions)
	}
	if versions[2].SourceNodeID != "sibling" || versions[1].SourceNodeID != "node" || ReadState(node).CurrentVersionID != versions[1].ID {
		t.Fatalf("legacy identity/current projection: %+v %+v", versions, ReadState(node))
	}
	if encode(sibling) != frozen {
		t.Fatal("import changed independently referenced sibling")
	}
	if metadata(node)["storageKey"] != "resource:old" {
		t.Fatal("legacy flags chose a different current resource")
	}
}
