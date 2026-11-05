package app

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// agentSessionDialects runs a Session case on a migrated SQLite database and,
// when CANVAS_TEST_POSTGRES_DSN is set, on an isolated PostgreSQL schema.
func agentSessionDialects(t *testing.T, run func(t *testing.T, s *Service, db *gorm.DB)) {
	t.Run("sqlite", func(t *testing.T) {
		db := newSQLiteTestDB(t)
		run(t, newAgentSessionService(t, db), db)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
		}
		db := platformPostgresDB(t, dsn)
		run(t, newAgentSessionService(t, db), db)
	})
}

func newAgentSessionService(t *testing.T, db *gorm.DB) *Service {
	t.Helper()
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	s := &Service{repo: repository.New(db), dataDir: t.TempDir(), activeCancels: make(map[string]context.CancelFunc)}
	for _, row := range []any{
		&model.CanvasProject{ID: "space-1", UserID: "user-a", Title: "画布 A", PayloadJSON: "{}"},
		&model.CanvasProject{ID: "canvas-b", UserID: "user-b", Title: "画布 B", PayloadJSON: "{}"},
		&model.Project{ID: "space-1", UserID: "user-a", Name: "短剧 A", Type: "short-drama", Status: model.ProjectStatusActive, Revision: 1},
		&model.Project{ID: "plain-1", UserID: "user-a", Name: "普通项目", Type: "canvas", Status: model.ProjectStatusActive, Revision: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func agentSessionCreate(t *testing.T, s *Service, user string, req as.CreateSessionRequest) as.SessionView {
	t.Helper()
	view, err := s.AgentSessions().Create(context.Background(), as.Caller{UserID: user}, req)
	if err != nil {
		t.Fatalf("create %+v: %v", req, err)
	}
	return view
}

func TestAgentSessionScopeIsolation(t *testing.T) {
	agentSessionDialects(t, func(t *testing.T, s *Service, db *gorm.DB) {
		ctx, a, b := context.Background(), as.Caller{UserID: "user-a"}, as.Caller{UserID: "user-b"}
		drama := as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "space-1"}
		canvas := as.Scope{Application: as.ApplicationCanvas, SpaceType: as.SpaceCanvas, SpaceID: "space-1"}

		// A1-A: two Sessions in one space are separate; the same short space ID in
		// another application is a different scope and never collides.
		first := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: drama, IdempotencyKey: "k1"})
		second := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: drama, IdempotencyKey: "k2"})
		onCanvas := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "canvas.agent", Scope: canvas, IdempotencyKey: "k3"})
		if first.ID == second.ID || first.ID == onCanvas.ID || onCanvas.Scope != canvas || first.Scope != drama {
			t.Fatalf("sessions collided: %+v %+v %+v", first, second, onCanvas)
		}
		replay := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: drama, IdempotencyKey: "k1"})
		if replay.ID != first.ID {
			t.Fatalf("replay created a new session: %s vs %s", replay.ID, first.ID)
		}
		if _, err := s.AgentSessions().Create(ctx, a, as.CreateSessionRequest{DefinitionID: "canvas.agent", Scope: canvas, IdempotencyKey: "k1"}); !as.IsCode(err, as.CodeIdempotencyConflict) {
			t.Fatalf("same key with other content: %v", err)
		}
		page, err := s.AgentSessions().List(ctx, a, as.ListSessionsRequest{Scope: &drama})
		if err != nil || len(page.Sessions) != 2 {
			t.Fatalf("list drama scope: %+v %v", page, err)
		}
		for _, view := range page.Sessions {
			if view.Scope != drama {
				t.Fatalf("list leaked another scope: %+v", view)
			}
		}

		// A1-B: another user can neither read nor archive the Session, and a forged
		// scope pointing at someone else's space grants nothing.
		if _, err := s.AgentSessions().Get(ctx, b, first.ID); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("foreign get: %v", err)
		}
		if _, err := s.AgentSessions().Archive(ctx, b, as.ArchiveSessionRequest{SessionID: first.ID, ExpectedRevision: first.Revision}); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("foreign archive: %v", err)
		}
		if page, err := s.AgentSessions().List(ctx, b, as.ListSessionsRequest{}); err != nil || len(page.Sessions) != 0 {
			t.Fatalf("foreign list: %+v %v", page, err)
		}
		forged := as.Scope{Application: as.ApplicationCanvas, SpaceType: as.SpaceCanvas, SpaceID: "canvas-b"}
		if _, err := s.AgentSessions().Create(ctx, a, as.CreateSessionRequest{DefinitionID: "canvas.agent", Scope: forged, IdempotencyKey: "forged"}); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("forged canvas scope: %v", err)
		}
		notDrama := as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "plain-1"}
		if _, err := s.AgentSessions().Create(ctx, a, as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: notDrama, IdempotencyKey: "plain"}); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("non-drama project accepted as drama space: %v", err)
		}

		// Archive needs the current revision and is visible only to the owner.
		if _, err := s.AgentSessions().Archive(ctx, a, as.ArchiveSessionRequest{SessionID: first.ID, ExpectedRevision: first.Revision + 5}); !as.IsCode(err, as.CodeSessionConflict) {
			t.Fatalf("stale archive: %v", err)
		}
		archived, err := s.AgentSessions().Archive(ctx, a, as.ArchiveSessionRequest{SessionID: first.ID, ExpectedRevision: first.Revision})
		if err != nil || archived.Status != as.SessionArchived || archived.Revision != first.Revision+1 {
			t.Fatalf("archive: %+v %v", archived, err)
		}
		var stored model.AgentSession
		if err := db.First(&stored, "id = ?", first.ID).Error; err != nil || stored.Application != as.ApplicationDrama || stored.SpaceID != "space-1" || stored.OwnerID != "user-a" {
			t.Fatalf("scope changed by archive: %+v %v", stored, err)
		}
	})
}

func TestAgentSessionSingleWriter(t *testing.T) {
	agentSessionDialects(t, func(t *testing.T, s *Service, db *gorm.DB) {
		scope := as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "space-1"}
		session := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: scope, IdempotencyKey: "writer"})

		// A1-C: twenty concurrent turns race for the writer; exactly one wins and
		// every loser gets an explicit session_conflict.
		const writers = 20
		var wg sync.WaitGroup
		results := make([]error, writers)
		leases := make([]as.Lease, writers)
		start := make(chan struct{})
		for i := range writers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				leases[i], results[i] = s.acquireAgentSessionWriter("user-a", session.ID, "worker", "run-"+string(rune('a'+i)))
			}(i)
		}
		close(start)
		wg.Wait()
		winners := 0
		var lease as.Lease
		for i, err := range results {
			switch {
			case err == nil:
				winners++
				lease = leases[i]
			case !as.IsCode(err, as.CodeSessionConflict):
				t.Fatalf("writer %d: unexpected error %v", i, err)
			}
		}
		if winners != 1 || lease.Epoch != 1 {
			t.Fatalf("winners=%d lease=%+v", winners, lease)
		}
		revision, err := s.commitAgentSessionWrite("user-a", session.ID, lease, session.Revision)
		if err != nil || revision != session.Revision+1 {
			t.Fatalf("current writer commit: %d %v", revision, err)
		}

		// The lease expires; a new writer takes over with a higher epoch and the
		// old writer's late write is rejected.
		if err := db.Model(&model.AgentSession{}).Where("id = ?", session.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		next, err := s.acquireAgentSessionWriter("user-a", session.ID, "worker-2", "run-next")
		if err != nil || next.Epoch != 2 {
			t.Fatalf("takeover: %+v %v", next, err)
		}
		if _, err := s.commitAgentSessionWrite("user-a", session.ID, lease, revision); !as.IsCode(err, as.CodeStaleLease) {
			t.Fatalf("old epoch write: %v", err)
		}
		if _, err := s.commitAgentSessionWrite("user-a", session.ID, next, revision-1); !as.IsCode(err, as.CodeSessionConflict) {
			t.Fatalf("stale revision write: %v", err)
		}
		if _, err := s.acquireAgentSessionWriter("user-b", session.ID, "worker", "run-x"); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("foreign writer: %v", err)
		}
		if _, err := s.AgentSessions().Archive(context.Background(), as.Caller{UserID: "user-a"}, as.ArchiveSessionRequest{SessionID: session.ID, ExpectedRevision: revision}); !as.IsCode(err, as.CodeSessionConflict) {
			t.Fatalf("archive while a writer is live: %v", err)
		}
		if err := s.releaseAgentSessionWriter("user-a", session.ID, next); err != nil {
			t.Fatal(err)
		}
		if _, err := s.commitAgentSessionWrite("user-a", session.ID, next, revision); !as.IsCode(err, as.CodeStaleLease) {
			t.Fatalf("write after release: %v", err)
		}
	})
}

func TestAgentSessionLegacyMapping(t *testing.T) {
	agentSessionDialects(t, func(t *testing.T, s *Service, db *gorm.DB) {
		base := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
		rows := []any{
			&model.CloudAgentExecution{ID: "run-root", UserID: "user-a", CanvasID: "space-1", Status: "succeeded", Title: "第一轮", CreatedAt: base, UpdatedAt: base},
			&model.CloudAgentExecution{ID: "run-next", UserID: "user-a", CanvasID: "space-1", ConversationID: "run-root", ParentID: "run-root", Status: "succeeded", Title: "第二轮", CreatedAt: base.Add(time.Minute), UpdatedAt: base.Add(time.Minute)},
			&model.CloudAgentExecution{ID: "run-child", UserID: "user-a", CanvasID: "space-1", Status: "succeeded", Title: "子代理", CreatedAt: base.Add(2 * time.Minute), UpdatedAt: base.Add(2 * time.Minute)},
			&model.AgentSubagentLink{ID: "link-1", UserID: "user-a", CanvasID: "space-1", ParentRunID: "run-root", ChildRunID: "run-child", IdempotencyKey: "link-1", DisplayName: "子代理", RoleLabel: "助手", Objective: "检查", Status: "succeeded"},
			&model.CloudAgentExecution{ID: "run-other", UserID: "user-b", CanvasID: "canvas-b", Status: "succeeded", Title: "他人", CreatedAt: base, UpdatedAt: base},
			&model.CloudAgentPiSession{ID: model.PiSessionStorageID("user-a", "run-root"), UserID: "user-a", ConversationID: "run-root", CanvasID: "space-1", HeaderJSON: "{}", Revision: 7},
		}
		for _, row := range rows {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		var piBefore model.CloudAgentPiSession
		if err := db.First(&piBefore, "conversation_id = ?", "run-root").Error; err != nil {
			t.Fatal(err)
		}

		// A1-D: mapping is idempotent and safe under concurrency: one Session per
		// canvas conversation, temporary subagents stay out, Pi storage untouched.
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.ensureCanvasAgentSessions("user-a"); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if err := s.ensureCanvasAgentSessions("user-a"); err != nil {
			t.Fatal(err)
		}
		var sessions []model.AgentSession
		if err := db.Where("owner_id = ?", "user-a").Find(&sessions).Error; err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 {
			t.Fatalf("want one mapped session, got %+v", sessions)
		}
		mapped := sessions[0]
		if mapped.ConversationID != "run-root" || mapped.Application != as.ApplicationCanvas || mapped.SpaceType != as.SpaceCanvas || mapped.SpaceID != "space-1" || mapped.DefinitionID != "canvas.agent" || mapped.Title != "第二轮" {
			t.Fatalf("mapped session: %+v", mapped)
		}
		var piAfter model.CloudAgentPiSession
		if err := db.First(&piAfter, "conversation_id = ?", "run-root").Error; err != nil || piAfter.ID != piBefore.ID || piAfter.Revision != piBefore.Revision {
			t.Fatalf("Pi session changed: %+v -> %+v %v", piBefore, piAfter, err)
		}
		page, err := s.AgentSessions().List(context.Background(), as.Caller{UserID: "user-a"}, as.ListSessionsRequest{})
		if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ConversationID != "run-root" {
			t.Fatalf("directory: %+v %v", page, err)
		}
		var foreign int64
		if err := db.Model(&model.AgentSession{}).Where("owner_id = ?", "user-b").Count(&foreign).Error; err != nil || foreign != 0 {
			t.Fatalf("mapping touched another owner: %d %v", foreign, err)
		}
	})
}

func TestAgentSessionNonCanvasNeedsNoCanvas(t *testing.T) {
	db := newSQLiteTestDB(t)
	s := newAgentSessionService(t, db)
	ctx, a := context.Background(), as.Caller{UserID: "user-a"}
	// A1-E: an ecommerce Session needs no canvas and no fake canvas row.
	batch := as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_01"}
	view := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "ecommerce.agent", Scope: batch, IdempotencyKey: "ecom"})
	if view.Scope != batch || view.ConversationID != "" {
		t.Fatalf("ecommerce session: %+v", view)
	}
	var canvases int64
	if err := db.Model(&model.CanvasProject{}).Count(&canvases).Error; err != nil || canvases != 2 {
		t.Fatalf("canvas rows changed: %d %v", canvases, err)
	}
	// Unknown definitions fail before any task or billing row exists.
	if _, err := s.AgentSessions().Create(ctx, a, as.CreateSessionRequest{DefinitionID: "ghost.agent", Scope: batch, IdempotencyKey: "ghost"}); !as.IsCode(err, as.CodeInvalidRequest) {
		t.Fatalf("unknown definition: %v", err)
	}
	if _, err := s.AgentSessions().Create(ctx, a, as.CreateSessionRequest{DefinitionID: "ecommerce.agent", Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "bad id!"}, IdempotencyKey: "bad"}); !as.IsCode(err, as.CodeInvalidRequest) {
		t.Fatalf("malformed batch id: %v", err)
	}
	var tasks, orders int64
	db.Model(&model.Task{}).Count(&tasks)
	db.Model(&model.BillingOrder{}).Count(&orders)
	if tasks != 0 || orders != 0 {
		t.Fatalf("session creation produced cost rows: tasks=%d orders=%d", tasks, orders)
	}
	defs, err := s.AgentSessions().Definitions(ctx, a, as.ApplicationEcommerce)
	if err != nil || len(defs) != 1 || defs[0].ID != "ecommerce.agent" {
		t.Fatalf("definitions: %+v %v", defs, err)
	}
	if _, err := s.AgentSessions().Create(ctx, as.Caller{}, as.CreateSessionRequest{DefinitionID: "ecommerce.agent", Scope: batch, IdempotencyKey: "anon"}); !as.IsCode(err, as.CodeUnauthorized) {
		t.Fatalf("anonymous create: %v", err)
	}
}
