package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	canvasdomain "yingce/backend/internal/canvas"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

type persistedEvent struct {
	Revision  int64  `json:"revision"`
	ActorKind string `json:"actorKind"`
}

func unifiedPersistFixture(t *testing.T) (*Service, *gorm.DB, *repository.Repository, Snapshot) {
	t.Helper()
	svc, db, _, snapshot := commandFixture(t)
	if err := db.AutoMigrate(&model.CanvasMutationReceipt{}); err != nil {
		t.Fatal(err)
	}
	return svc, db, repository.New(db), snapshot
}

// browserPatchTitle 走浏览器的 operations 写入路径（上游 ApplyUserCanvasOperations）。
func browserPatchTitle(t *testing.T, repo *repository.Repository, canvasID, nodeID, title, mutationID string) {
	t.Helper()
	row, err := repo.CanvasProjectForUser("owner", canvasID)
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal([]byte(row.PayloadJSON), &doc); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(entity(doc, "node:"+nodeID)["title"])
	value, _ := json.Marshal(title)
	revision := row.Revision
	req := canvasdomain.CanvasOperationsRequest{MutationID: mutationID, BaseRevision: &revision, Operations: []canvasdomain.CanvasOperation{{
		Type: "patch_node", ID: nodeID, Patch: map[string]json.RawMessage{"title": value}, Before: map[string]json.RawMessage{"title": before},
	}}}
	if _, err := canvasdomain.New(repo, nil).ApplyUserCanvasOperations("owner", canvasID, req); err != nil {
		t.Fatal(err)
	}
}

func decodedEvents(t *testing.T, repo *repository.Repository, canvasID string) []persistedEvent {
	t.Helper()
	rows, err := repo.CommandEvents("owner", canvasID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := []persistedEvent{}
	for _, row := range rows {
		var event persistedEvent
		if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
			t.Fatal(err)
		}
		out = append(out, event)
	}
	return out
}

func TestAgentAndBrowserShareRevisionSequence(t *testing.T) {
	svc, _, repo, snapshot := unifiedPersistFixture(t)
	browserPatchTitle(t, repo, snapshot.CanvasID, "a", "browser one", "browser-1")
	current, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, snapshot.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, edit(current, "b", "agent", "agent-1")); err != nil {
		t.Fatal(err)
	}
	browserPatchTitle(t, repo, snapshot.CanvasID, "a", "browser two", "browser-2")

	events := decodedEvents(t, repo, snapshot.CanvasID)
	if len(events) < 3 {
		t.Fatalf("expected at least three events, got %+v", events)
	}
	tail := events[len(events)-3:]
	n := tail[0].Revision
	for i, event := range tail {
		if event.Revision != n+int64(i) {
			t.Fatalf("event revisions are not contiguous: %+v", tail)
		}
	}
	if tail[1].ActorKind != "agent" {
		t.Fatalf("agent event actorKind = %q, want agent", tail[1].ActorKind)
	}
	if tail[0].ActorKind == "agent" || tail[2].ActorKind == "agent" {
		t.Fatalf("browser events attributed to agent: %+v", tail)
	}
	row, err := repo.CanvasProjectForUser("owner", snapshot.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Revision != tail[2].Revision {
		t.Fatalf("canvas revision %d does not match last event %d", row.Revision, tail[2].Revision)
	}
}

// 基线已满足：浏览器写入经投影更新实体版本，Agent 的完整版本 CAS 能看到它。保留为防回归。
func TestAgentWriteStaleRevisionConflicts(t *testing.T) {
	svc, _, repo, snapshot := unifiedPersistFixture(t)
	browserPatchTitle(t, repo, snapshot.CanvasID, "a", "browser edit", "browser-stale")
	req := edit(snapshot, "a", "agent", "agent-stale")
	req.ExpectedEntityVersions = snapshot.EntityVersions
	_, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	var failure *Error
	if !errors.As(err, &failure) || failure.Reason != "entity_version_conflict" {
		t.Fatalf("stale agent write err = %v, want entity_version_conflict", err)
	}
}

// Agent 整份写回（无命令层事件）同样进入共享 revision 序列，并由仓储投影为 Agent 事件。
func TestAgentFullDocumentWriteSharesRevisionSequence(t *testing.T) {
	_, _, repo, snapshot := unifiedPersistFixture(t)
	before, err := repo.CanvasProjectForUser("owner", snapshot.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal([]byte(before.PayloadJSON), &doc); err != nil {
		t.Fatal(err)
	}
	entity(doc, "node:a")["title"] = "agent full write"
	raw, _ := json.Marshal(doc)
	after := *before
	after.PayloadJSON = string(raw)
	err = repo.Transaction(func(tx *repository.Repository) error {
		return tx.SaveAgentCanvasWrite(before, &after, repository.AgentActor{RunID: "run-1", UserID: "owner"}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("revision = %d, want %d", after.Revision, before.Revision+1)
	}
	events := decodedEvents(t, repo, snapshot.CanvasID)
	last := events[len(events)-1]
	if last.Revision != after.Revision || last.ActorKind != "agent" {
		t.Fatalf("projected event = %+v, want revision %d actor agent", last, after.Revision)
	}
	stale := *before
	stale.PayloadJSON = string(raw)
	err = repo.Transaction(func(tx *repository.Repository) error {
		return tx.SaveAgentCanvasWrite(before, &stale, repository.AgentActor{UserID: "owner"}, nil)
	})
	if !errors.Is(err, repository.ErrCanvasRevisionConflict) {
		t.Fatal(fmt.Errorf("stale full write err = %v, want revision conflict", err))
	}
}
