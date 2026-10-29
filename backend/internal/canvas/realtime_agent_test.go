package canvas

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func noRealtimeEvent(t *testing.T, session *RealtimeSession, match func(RealtimeEvent) bool) {
	t.Helper()
	for {
		select {
		case event := <-session.Events:
			if match(event) {
				t.Fatalf("unexpected realtime event: %+v", event)
			}
		default:
			return
		}
	}
}

func TestAgentPresenceBroadcast(t *testing.T) {
	hub := NewRealtimeHub(nil)
	a, _, err := hub.Open(context.Background(), "user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	other, _, _ := hub.Open(context.Background(), "user", "other")
	defer other.Close()
	foreign, _, _ := hub.Open(context.Background(), "intruder", "canvas")
	defer foreign.Close()

	hub.AgentPresence("user", "canvas", AgentPresence{RunID: "run", NodeIDs: []string{"node-a", "node-a", "", "node-b"}, Phase: "editing"})
	event := waitPresenceEvent(t, a, func(e RealtimeEvent) bool { return e.Type == "agent_presence" })
	if event.Agent == nil || event.Agent.RunID != "run" || event.Agent.Phase != "editing" || len(event.Agent.NodeIDs) != 2 || event.Agent.NodeIDs[0] != "node-a" || event.Agent.NodeIDs[1] != "node-b" {
		t.Fatalf("agent presence not normalized: %+v", event.Agent)
	}
	isAgent := func(e RealtimeEvent) bool { return e.Type == "agent_presence" }
	noRealtimeEvent(t, other, isAgent)
	noRealtimeEvent(t, foreign, isAgent)

	// Unknown phases and anonymous runs never reach clients.
	hub.AgentPresence("user", "canvas", AgentPresence{RunID: "run", NodeIDs: []string{"node-a"}, Phase: "deleting"})
	hub.AgentPresence("user", "canvas", AgentPresence{NodeIDs: []string{"node-a"}, Phase: "reading"})
	noRealtimeEvent(t, a, isAgent)

	many := make([]string, 0, 300)
	for index := 0; index < 300; index++ {
		many = append(many, string(rune('a'+index%26))+time.Duration(index).String())
	}
	hub.AgentPresence("user", "canvas", AgentPresence{RunID: "run", NodeIDs: many, Phase: "reading"})
	event = waitPresenceEvent(t, a, isAgent)
	if len(event.Agent.NodeIDs) != maxAgentPresenceNodes {
		t.Fatalf("presence node list is unbounded: %d", len(event.Agent.NodeIDs))
	}

	hub.AgentPresence("user", "canvas", AgentPresence{RunID: "run", NodeIDs: []string{"node-a"}, Phase: "idle"})
	event = waitPresenceEvent(t, a, isAgent)
	if event.Agent.Phase != "idle" || len(event.Agent.NodeIDs) != 0 {
		t.Fatalf("idle presence must clear nodes: %+v", event.Agent)
	}
}

func realtimeAgentTestRepository(t *testing.T, hub *RealtimeHub) *repository.Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "realtime.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&model.User{}, &model.CanvasEditLease{}, &model.CanvasProject{}, &model.CanvasUnitLink{}, &model.CanvasShare{},
		&model.Project{}, &model.ProjectUnit{}, &model.Task{},
		&model.CanvasDocumentEpoch{}, &model.CanvasEntityVersion{}, &model.CanvasCommandReceipt{}, &model.CanvasCommandEvent{},
		&model.CanvasActorHistory{}, &model.CanvasCommandResource{}, &model.CanvasCreateReceipt{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "user", Username: "user"}).Error; err != nil {
		t.Fatal(err)
	}
	return repository.New(db).WithCanvasChangeNotifier(func(change repository.CanvasChange) {
		hub.CanvasChanged(change.UserID, change.CanvasID, change.Revision, change.Actor)
	})
}

func TestAgentWritePublishesCanvasChanged(t *testing.T) {
	hub := NewRealtimeHub(nil)
	repo := realtimeAgentTestRepository(t, hub)
	session, _, err := hub.Open(context.Background(), "user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	changed := func(e RealtimeEvent) bool { return e.Type == "canvas_changed" }

	created := &model.CanvasProject{ID: "canvas", UserID: "user", Title: "画布", PayloadJSON: `{"id":"canvas","nodes":[{"id":"a","type":"text","title":"A"}]}`}
	if err := repo.UpsertCanvasProject(created); err != nil {
		t.Fatal(err)
	}
	event := waitPresenceEvent(t, session, changed)
	if event.Revision != 1 || event.Actor != "browser" {
		t.Fatalf("browser save event = %+v", event)
	}

	before, err := repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	after := *before
	after.PayloadJSON = `{"id":"canvas","nodes":[{"id":"a","type":"text","title":"Agent A"}]}`
	// Agent writes usually join an outer checkpoint transaction; nothing may be
	// published until that outer transaction commits.
	err = repo.CommandTransaction(context.Background(), func(tx *repository.Repository) error {
		if err := tx.SaveAgentCanvasWrite(before, &after, repository.AgentActor{RunID: "run", UserID: "user"}, nil); err != nil {
			return err
		}
		noRealtimeEvent(t, session, changed)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	event = waitPresenceEvent(t, session, changed)
	if event.Revision != 2 || event.Actor != "agent" {
		t.Fatalf("agent write event = %+v", event)
	}

	// A rolled-back Agent write is never announced.
	current, _ := repo.CanvasProjectForUser("user", "canvas")
	rolled := *current
	rolled.PayloadJSON = `{"id":"canvas","nodes":[]}`
	_ = repo.CommandTransaction(context.Background(), func(tx *repository.Repository) error {
		if err := tx.SaveAgentCanvasWrite(current, &rolled, repository.AgentActor{RunID: "run", UserID: "user"}, nil); err != nil {
			return err
		}
		return context.Canceled
	})
	noRealtimeEvent(t, session, changed)

	// Lease-only changes keep the revision-less invalidate contract.
	_, err = repo.ChangeCanvasEditLease("user", "canvas", model.CanvasEditProof{NodeID: "a", SessionID: "window"}, "acquire", "token", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	waitPresenceEvent(t, session, func(e RealtimeEvent) bool { return e.Type == "invalidate" && e.Revision == 0 })
}
