package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	canvasdomain "yingce/backend/internal/canvas"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCommandVersionSnapshotsCaptureInitialThrottleHostAndRetention(t *testing.T) {
	svc, db, _, before := parityCommandFixture(t)
	repo := repository.New(db)
	domain := canvasdomain.New(repo, nil)
	ctx, p := context.Background(), ownerContext().Principal
	create := CreateCanvasRequest{ClientID: "client", OperationID: "initial-version", InitialOperations: []Operation{{Type: "add_node", ID: "initial-node", NodeType: "text", X: num(0), Y: num(0)}}}
	created, err := svc.Create(ctx, p, create)
	if err != nil {
		t.Fatal(err)
	}
	createdHistory, err := domain.CanvasHistory("owner", created.CanvasID)
	if err != nil || len(createdHistory.Snapshots) != 1 || createdHistory.Snapshots[0].NodeCount != 1 || createdHistory.Snapshots[0].Revision != created.Revision {
		t.Fatalf("initial operations missing from history: %+v %v", createdHistory, err)
	}
	if _, err = svc.Create(ctx, p, create); err != nil {
		t.Fatal(err)
	}
	createReplay, err := domain.CanvasHistory("owner", created.CanvasID)
	if err != nil || len(createReplay.Snapshots) != 1 {
		t.Fatalf("create replay duplicated history: %+v %v", createReplay, err)
	}
	initial, err := domain.CanvasHistory("owner", before.CanvasID)
	if err != nil || len(initial.Snapshots) != 1 || initial.Snapshots[0].Revision != 1 {
		t.Fatalf("command-only initial history: %+v %v", initial, err)
	}
	first, err := svc.Execute(ctx, p, edit(before, "a", "first command save", "history-first"))
	if err != nil {
		t.Fatal(err)
	}
	throttled, err := domain.CanvasHistory("owner", before.CanvasID)
	if err != nil || len(throttled.Snapshots) != 1 {
		t.Fatalf("ordinary capture interval ignored: %+v %v", throttled, err)
	}
	if err = db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", before.CanvasID).Update("created_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	current, err := svc.ReadSnapshot(ctx, p, before.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	req := edit(current, "a", "after interval", "history-after-interval")
	receipt, err := svc.Execute(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	after, err := domain.CanvasHistory("owner", before.CanvasID)
	if err != nil || len(after.Snapshots) != 2 || after.Snapshots[0].Revision != first.Revision {
		t.Fatalf("command save missing from UI history: %+v %v", after, err)
	}
	if _, err = svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	replayed, err := domain.CanvasHistory("owner", before.CanvasID)
	if err != nil || len(replayed.Snapshots) != 2 {
		t.Fatalf("replay duplicated snapshots: %+v %v", replayed, err)
	}
	if err = db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", before.CanvasID).Update("created_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	hostReq := Command{Protocol: 1, CanvasID: before.CanvasID, DocumentEpoch: before.DocumentEpoch, ClientID: "client", OperationID: "history-host", BaseRevision: receipt.Revision}
	_, fresh, err := svc.ExecuteHost(ctx, p, hostReq, "task_result", nil, false, func(_ *repository.Repository, doc Document) (Document, map[string]bool, error) {
		maps(doc["nodes"])[0]["metadata"].(map[string]any)["content"] = "host result"
		return doc, nil, nil
	})
	if err != nil || !fresh {
		t.Fatalf("host persist: %v", err)
	}
	hostHistory, err := domain.CanvasHistory("owner", before.CanvasID)
	if err != nil || len(hostHistory.Snapshots) != 3 || hostHistory.Snapshots[0].Revision != receipt.Revision {
		t.Fatalf("host result missing from UI history: %+v %v", hostHistory, err)
	}
	// The next eligible actual save must apply the same bounded retention.
	for i := 0; i < 22; i++ {
		if err = db.Create(&model.CanvasSnapshot{ID: fmt.Sprintf("retention-%d", i), CanvasID: before.CanvasID, UserID: "owner", Revision: -int64(i + 1), PayloadJSON: `{"nodes":[]}`, CreatedAt: time.Now().Add(-time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", before.CanvasID).Update("created_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	current, err = svc.ReadSnapshot(ctx, p, before.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Execute(ctx, p, edit(current, "a", "retention save", "history-retention")); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err = db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", before.CanvasID).Count(&count).Error; err != nil || count != 20 {
		t.Fatalf("snapshot retention count=%d %v", count, err)
	}
}

func TestCommandRestorePreservesCurrentMetadataAndVisibleBackup(t *testing.T) {
	for _, association := range []string{"moved", "independent"} {
		t.Run(association, func(t *testing.T) {
			svc, db, _, before := parityCommandFixture(t)
			repo := repository.New(db)
			domain := canvasdomain.New(repo, nil)
			currentProject := "current-project"
			if association == "independent" {
				currentProject = ""
			}
			if err := db.Model(&model.CanvasProject{}).Where("id = ?", before.CanvasID).Update("project_id", currentProject).Error; err != nil {
				t.Fatal(err)
			}
			row, err := repo.CanvasProjectForUser("owner", before.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			historical, err := cloneDocument(before.Document)
			if err != nil {
				t.Fatal(err)
			}
			historical["projectId"], historical["createdAt"] = "old-project", "2000-01-01T00:00:00Z"
			maps(historical["nodes"])[0]["metadata"].(map[string]any)["content"] = "historical content"
			ctx, p := context.Background(), ownerContext().Principal
			restored, err := svc.RestoreVersion(ctx, p, before.CanvasID, before.DocumentEpoch, "client", "restore-current-metadata", before.Revision, historical)
			if err != nil {
				t.Fatal(err)
			}
			created, parseErr := time.Parse(time.RFC3339Nano, text(restored.Document["createdAt"]))
			if restored.Document["projectId"] != currentProject || parseErr != nil || !created.Equal(row.CreatedAt) {
				t.Errorf("restored metadata diverges: project=%v created=%v authority=%s/%s", restored.Document["projectId"], restored.Document["createdAt"], currentProject, row.CreatedAt)
			}
			stored, err := repo.CanvasProjectForUser("owner", before.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			var payload Document
			if err = json.Unmarshal([]byte(stored.PayloadJSON), &payload); err != nil {
				t.Fatal(err)
			}
			if stored.ProjectID != currentProject || !stored.CreatedAt.Equal(row.CreatedAt) || payload["projectId"] != currentProject {
				t.Errorf("persisted restore metadata: %+v payload.projectId=%v", stored, payload["projectId"])
			}
			history, err := domain.CanvasHistory("owner", before.CanvasID)
			if err != nil || len(history.Snapshots) == 0 || history.Snapshots[0].Revision != before.Revision || history.Snapshots[0].Reason != "before_restore" {
				t.Fatalf("pre-restore backup is not selectable: %+v %v", history, err)
			}
			backup, err := domain.CanvasHistorySnapshot("owner", before.CanvasID, history.Snapshots[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			var backupDoc Document
			if err = json.Unmarshal([]byte(backup.PayloadJSON), &backupDoc); err != nil {
				t.Fatal(err)
			}
			if backupDoc["projectId"] != currentProject || maps(backupDoc["nodes"])[0]["metadata"].(map[string]any)["content"] != "" {
				t.Fatalf("backup is not the authoritative preimage: %+v", backupDoc)
			}
			replay, err := svc.RestoreVersion(ctx, p, before.CanvasID, before.DocumentEpoch, "client", "restore-current-metadata", before.Revision, historical)
			if err != nil || !reflect.DeepEqual(restored, replay) {
				t.Fatalf("restore replay changed: %+v %v", replay, err)
			}
			replayed, err := domain.CanvasHistory("owner", before.CanvasID)
			if err != nil || len(replayed.Snapshots) != len(history.Snapshots) {
				t.Fatalf("restore replay duplicated history: %+v %v", replayed, err)
			}
		})
	}
}

func TestCommandSnapshotFailureRollsBackHostDocumentReceiptAndSideEffects(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	if err := db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", before.CanvasID).Update("created_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER fail_command_snapshot BEFORE INSERT ON canvas_snapshots BEGIN SELECT RAISE(ABORT, 'snapshot unavailable'); END;`).Error; err != nil {
		t.Fatal(err)
	}
	req := Command{Protocol: 1, CanvasID: before.CanvasID, DocumentEpoch: before.DocumentEpoch, ClientID: "client", OperationID: "snapshot-failure", BaseRevision: before.Revision}
	_, fresh, err := svc.ExecuteHost(context.Background(), ownerContext().Principal, req, "task_result", nil, false, func(tx *repository.Repository, doc Document) (Document, map[string]bool, error) {
		if err := tx.Create(&model.Task{ID: "snapshot-task", UserID: "owner"}); err != nil {
			return nil, nil, err
		}
		maps(doc["nodes"])[0]["metadata"].(map[string]any)["content"] = "must roll back"
		return doc, nil, nil
	})
	if err == nil || fresh {
		t.Fatalf("snapshot failure acknowledged: fresh=%v err=%v", fresh, err)
	}
	after, readErr := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if readErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("snapshot failure partially committed document: %+v %v", after, readErr)
	}
	for _, target := range []struct {
		model any
		where string
		value string
	}{{&model.Task{}, "id = ?", "snapshot-task"}, {&model.CanvasCommandReceipt{}, "operation_id = ?", req.OperationID}, {&model.CanvasCommandEvent{}, "operation_id = ?", req.OperationID}} {
		var count int64
		if err := db.Model(target.model).Where(target.where, target.value).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("snapshot failure partially committed %T: %d %v", target.model, count, err)
		}
	}
}
