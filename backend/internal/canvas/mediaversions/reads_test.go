package mediaversions

import (
	"fmt"
	"strings"
	"testing"
	"yingce/backend/internal/model"
)

func TestMediaVersionReadPaginatesWithoutCredentialsOrProviderPayload(t *testing.T) {
	repo, scope, node := versionFixture(t, "image")
	for seq := int64(1); seq <= 25; seq++ {
		row := model.CanvasMediaVersion{ID: fmt.Sprintf("v%d", seq), OwnerID: scope.OwnerID, CanvasID: scope.CanvasID, Epoch: scope.Epoch, NodeID: scope.NodeID, Generation: scope.Generation, Sequence: seq, Mode: "image", Status: "ready", ResourceID: "old", MediaJSON: `{"storageKey":"resource:old","content":"https://private.invalid/image?token=private-sentinel"}`, RequestJSON: `{"prompt":"frozen prompt","model":"resolved","apiKey":"private-sentinel","input":{"config":{"quality":"high","apiKey":"private-sentinel","headers":{"Authorization":"private-sentinel"}}}}`, OutputsJSON: encode([]model.CanvasTaskOutput{{OutputIndex: 0, Kind: "image", ResourceID: "old", PayloadJSON: `{"data":{"url":"https://private.invalid/image?token=private-sentinel","storageKey":"resource:old","providerResponse":"private-sentinel"}}`}})}
		if err := repo.InsertCanvasMediaVersion(&row); err != nil {
			t.Fatal(err)
		}
	}
	first, err := Read(repo, scope, node, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 20 || first.Items[0].Sequence != 25 || first.NextBeforeVersion != 6 {
		t.Fatalf("first page: %+v", first)
	}
	second, err := Read(repo, scope, node, first.NextBeforeVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 5 || second.Items[0].Sequence != 5 || second.NextBeforeVersion != 0 {
		t.Fatalf("second page: %+v", second)
	}
	if strings.Contains(encode(first), "private-sentinel") || first.Items[0].Prompt != "frozen prompt" || first.Items[0].Options["quality"] != "high" {
		t.Fatal("read leaked provider data or lost public frozen specs")
	}
	other := scope
	other.OwnerID = "other"
	foreign, err := Read(repo, other, node, 0)
	if err != nil || len(foreign.Items) != 0 {
		t.Fatal("read crossed owner scope", err)
	}
}
