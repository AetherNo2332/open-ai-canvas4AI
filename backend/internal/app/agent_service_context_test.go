package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
)

func agentContextText(snapshot as.ContextSnapshot) string {
	parts := []string{}
	for _, block := range snapshot.Blocks {
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n")
}

// agentContextAppend writes one turn through the single-writer path.
func agentContextAppend(t *testing.T, s *Service, owner, sessionID string, messages ...as.HistoryMessage) {
	t.Helper()
	session, err := s.agentSessionForOwner(owner, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.acquireAgentSessionWriter(owner, sessionID, "test-worker", "run-"+sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.appendAgentSessionMessages(owner, sessionID, lease, session.Revision, messages); err != nil {
		t.Fatal(err)
	}
	if err := s.releaseAgentSessionWriter(owner, sessionID, lease); err != nil {
		t.Fatal(err)
	}
}

func agentUserText(id, text string) as.HistoryMessage {
	return as.HistoryMessage{ID: id, Kind: as.MessageKindMessage, Actor: as.ActorUser, Role: "user", Text: text}
}

func TestAgentContextScopeCanaries(t *testing.T) {
	agentSessionDialects(t, func(t *testing.T, s *Service, db *gorm.DB) {
		drama := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "space-1"}, IdempotencyKey: "drama"})
		ecommerce := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "ecommerce.agent", Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_0001"}, IdempotencyKey: "ecom"})
		foreign := agentSessionCreate(t, s, "user-b", as.CreateSessionRequest{DefinitionID: "canvas.agent", Scope: as.Scope{Application: as.ApplicationCanvas, SpaceType: as.SpaceCanvas, SpaceID: "canvas-b"}, IdempotencyKey: "b"})
		agentContextAppend(t, s, "user-a", drama.ID, agentUserText("d1", "DRAMA-CANARY 第一集剧情"))
		agentContextAppend(t, s, "user-a", ecommerce.ID, agentUserText("e1", "ECOM-CANARY 白底陶瓷杯"))
		agentContextAppend(t, s, "user-b", foreign.ID, agentUserText("b1", "FOREIGN-CANARY"))
		if _, err := s.writeAgentSessionNote("user-a", drama.ID, "DRAMA-NOTE-CANARY"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.writeAgentSessionNote("user-a", ecommerce.ID, "ECOM-NOTE-CANARY"); err != nil {
			t.Fatal(err)
		}

		// A2-A: each final input contains only its own Session's material.
		dramaContext, err := s.buildAgentSessionContext("user-a", drama.ID)
		if err != nil {
			t.Fatal(err)
		}
		ecomContext, err := s.buildAgentSessionContext("user-a", ecommerce.ID)
		if err != nil {
			t.Fatal(err)
		}
		for name, check := range map[string]struct {
			text      string
			want, not []string
		}{
			"drama":     {agentContextText(dramaContext), []string{"DRAMA-CANARY", "DRAMA-NOTE-CANARY"}, []string{"ECOM-CANARY", "ECOM-NOTE-CANARY", "FOREIGN-CANARY"}},
			"ecommerce": {agentContextText(ecomContext), []string{"ECOM-CANARY", "ECOM-NOTE-CANARY"}, []string{"DRAMA-CANARY", "DRAMA-NOTE-CANARY", "FOREIGN-CANARY"}},
		} {
			for _, want := range check.want {
				if !strings.Contains(check.text, want) {
					t.Fatalf("%s context lacks %s:\n%s", name, want, check.text)
				}
			}
			for _, not := range check.not {
				if strings.Contains(check.text, not) {
					t.Fatalf("%s context leaked %s:\n%s", name, not, check.text)
				}
			}
		}
		// A2-D: every block carries the Session identity and source.
		for _, block := range dramaContext.Blocks {
			if block.SessionID != drama.ID || block.Scope != drama.Scope || block.Source == "" {
				t.Fatalf("untraceable block: %+v", block)
			}
		}
		if _, err := s.buildAgentSessionContext("user-b", drama.ID); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("foreign build: %v", err)
		}
	})
}

func TestAgentMemoryPublicationBoundary(t *testing.T) {
	agentSessionDialects(t, func(t *testing.T, s *Service, db *gorm.DB) {
		scope := as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "space-1"}
		first := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: scope, IdempotencyKey: "first"})
		second := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: scope, IdempotencyKey: "second"})
		elsewhere := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "canvas.agent", Scope: as.Scope{Application: as.ApplicationCanvas, SpaceType: as.SpaceCanvas, SpaceID: "space-1"}, IdempotencyKey: "canvas"})
		note, err := s.writeAgentSessionNote("user-a", first.ID, "PRIVATE-NOTE")
		if err != nil {
			t.Fatal(err)
		}
		agentContextAppend(t, s, "user-a", first.ID, as.HistoryMessage{ID: "sum", Kind: as.MessageKindSummary, Actor: as.ActorSystem, Role: "user", Text: "PRIVATE-SUMMARY"})

		// A2-B: same space, different Session: private notes and summaries stay private.
		secondContext, err := s.buildAgentSessionContext("user-a", second.ID)
		if err != nil {
			t.Fatal(err)
		}
		if text := agentContextText(secondContext); strings.Contains(text, "PRIVATE-NOTE") || strings.Contains(text, "PRIVATE-SUMMARY") {
			t.Fatalf("private material leaked to sibling session:\n%s", text)
		}

		// Publishing makes it a document of exactly this space, idempotently.
		published, err := s.publishAgentSessionNote("user-a", note.ID)
		if err != nil {
			t.Fatal(err)
		}
		again, err := s.publishAgentSessionNote("user-a", note.ID)
		if err != nil || again.ID != published.ID {
			t.Fatalf("republish: %+v %v", again, err)
		}
		secondContext, err = s.buildAgentSessionContext("user-a", second.ID)
		if err != nil || !strings.Contains(agentContextText(secondContext), "PRIVATE-NOTE") {
			t.Fatalf("published note missing in same space: %v", err)
		}
		elsewhereContext, err := s.buildAgentSessionContext("user-a", elsewhere.ID)
		if err != nil || strings.Contains(agentContextText(elsewhereContext), "PRIVATE-NOTE") {
			t.Fatalf("published note leaked to another application with the same space ID: %v", err)
		}
		if _, err := s.publishAgentSessionNote("user-b", note.ID); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("foreign publish: %v", err)
		}
		var spaceDocs int64
		if err := db.Model(&model.AgentMemoryNote{}).Where("kind = ?", as.MemoryBusinessSpace).Count(&spaceDocs).Error; err != nil || spaceDocs != 1 {
			t.Fatalf("space documents: %d %v", spaceDocs, err)
		}
	})
}

func TestAgentLegacyMemoryQuarantine(t *testing.T) {
	agentSessionDialects(t, func(t *testing.T, s *Service, db *gorm.DB) {
		legacy := model.AgentMemoryDocument{UserID: "user-a", EntriesJSON: `[{"id":"notes-1","section":"notes","text":"LEGACY-CANARY 另一个项目的笔记"}]`, NextEntrySeq: 2, Revision: 1, Mode: "auto", Capacity: 5000}
		if err := db.Create(&legacy).Error; err != nil {
			t.Fatal(err)
		}
		session := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "space-1"}, IdempotencyKey: "fresh"})
		if _, err := s.writeAgentUserPreference("user-a", "PREF-CANARY 回复用简体中文"); err != nil {
			t.Fatal(err)
		}
		snapshot, err := s.buildAgentSessionContext("user-a", session.ID)
		if err != nil {
			t.Fatal(err)
		}
		// A2-C: the legacy per-user document is never injected; explicit user preferences are.
		text := agentContextText(snapshot)
		if strings.Contains(text, "LEGACY-CANARY") || !strings.Contains(text, "PREF-CANARY") {
			t.Fatalf("legacy quarantine:\n%s", text)
		}
		doc, err := s.repo.AgentMemoryDocument("user-a")
		if err != nil || !strings.Contains(doc.EntriesJSON, "LEGACY-CANARY") {
			t.Fatalf("legacy record must stay viewable: %v", err)
		}
	})
}

func TestAgentContextRestoreScope(t *testing.T) {
	agentSessionDialects(t, func(t *testing.T, s *Service, db *gorm.DB) {
		scope := as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "space-1"}
		session := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: scope, IdempotencyKey: "restore"})
		lease, err := s.acquireAgentSessionWriter("user-a", session.ID, "worker", "run-1")
		if err != nil {
			t.Fatal(err)
		}
		revision, err := s.appendAgentSessionMessages("user-a", session.ID, lease, session.Revision, []as.HistoryMessage{agentUserText("m1", "第一条")})
		if err != nil || revision != session.Revision+1 {
			t.Fatalf("append: %d %v", revision, err)
		}
		// Replaying the same message ID is a no-op, not a second message.
		if again, err := s.appendAgentSessionMessages("user-a", session.ID, lease, revision, []as.HistoryMessage{agentUserText("m1", "第一条")}); err != nil || again != revision {
			t.Fatalf("replay: %d %v", again, err)
		}
		// A2-D: a writer whose lease was taken over cannot append, even with a valid revision.
		if err := db.Model(&model.AgentSession{}).Where("id = ?", session.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := s.acquireAgentSessionWriter("user-a", session.ID, "worker-2", "run-2"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.appendAgentSessionMessages("user-a", session.ID, lease, revision, []as.HistoryMessage{agentUserText("m2", "迟到的旧写入")}); !as.IsCode(err, as.CodeStaleLease) {
			t.Fatalf("old writer append: %v", err)
		}
		// A row tampered to point elsewhere is not read into this Session.
		if err := db.Create(&model.AgentSessionMessage{ID: "stray", OwnerID: "user-a", SessionID: "another", MessageID: "x", Sequence: 9, Kind: as.MessageKindSummary, Actor: as.ActorSystem, Role: "user", Body: "STRAY-SUMMARY"}).Error; err != nil {
			t.Fatal(err)
		}
		snapshot, err := s.buildAgentSessionContext("user-a", session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if text := agentContextText(snapshot); strings.Contains(text, "迟到的旧写入") || strings.Contains(text, "STRAY-SUMMARY") || !strings.Contains(text, "第一条") {
			t.Fatalf("restored context:\n%s", text)
		}
		if snapshot.SessionRevision != revision {
			t.Fatalf("snapshot revision %d, want %d", snapshot.SessionRevision, revision)
		}
		// A new Session in the same space starts empty: no inherited summary.
		fresh := agentSessionCreate(t, s, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: scope, IdempotencyKey: "fresh"})
		freshContext, err := s.buildAgentSessionContext("user-a", fresh.ID)
		if err != nil || strings.Contains(agentContextText(freshContext), "第一条") {
			t.Fatalf("fresh session inherited history: %v", err)
		}
		if _, err := s.buildAgentSessionContext("user-a", "missing"); !as.IsCode(err, as.CodeNotFound) {
			t.Fatalf("missing session: %v", err)
		}
		if _, err := s.AgentSessions().Archive(context.Background(), as.Caller{UserID: "user-a"}, as.ArchiveSessionRequest{SessionID: fresh.ID, ExpectedRevision: fresh.Revision}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.writeAgentSessionNote("user-a", fresh.ID, "写入已归档会话"); !as.IsCode(err, as.CodeSessionConflict) {
			t.Fatalf("note on archived session: %v", err)
		}
	})
}
