package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

// The HTTP auth middleware must reject both an invalid worker credential and a
// valid grant whose live lease has since been taken over before any application
// write path is reached. The persisted run, session, transcript and Pi receipts
// are compared as a whole so a handler that writes first and maps the error later
// cannot pass this test.
func TestInternalAgentAuthRejectsWithoutWrites(t *testing.T) {
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("k", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "auth-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("CANVAS_AGENT_WORKER_USER_ID", "auth-user")
	t.Setenv("CANVAS_AGENT_WAKE_MODE", "poll")
	t.Setenv("REDIS_URL", "")

	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), t.TempDir())
	t.Cleanup(func() { _ = svc.Close() })
	if err := svc.SeedAgentWorker(); err != nil {
		t.Fatal(err)
	}

	const (
		userID = "auth-user"
		runID  = "auth-run"
		owner  = "auth-worker:slot"
		epoch  = int64(3)
	)
	expires := time.Now().UTC().Add(time.Hour)
	run := model.CloudAgentExecution{
		ID:             runID,
		UserID:         userID,
		Status:         "running",
		ConversationID: "auth-conversation",
		Engine:         "pi",
		LeaseOwner:     owner,
		LeaseExpiresAt: &expires,
		Revision:       7,
		EventCount:     2,
		MessageCount:   1,
		StateJSON:      `{"marker":"before"}`,
	}
	session := model.CloudAgentPiSession{
		ID:             model.PiSessionStorageID(userID, run.ConversationID),
		UserID:         userID,
		ConversationID: run.ConversationID,
		CanvasID:       "auth-canvas",
		FormatVersion:  3,
		HeaderJSON:     `{"type":"session","version":3}`,
		ActiveRunID:    runID,
		LeaseOwner:     owner,
		LeaseEpoch:     epoch,
		LeaseExpiresAt: &expires,
		Revision:       4,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentMessageRecord{
		RunID: runID, Kind: "canonical", Sequence: 1, UserID: userID,
		MessageJSON: `{"role":"user","content":"before"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentEventRecord{
		RunID: runID, Sequence: 1, UserID: userID,
		EventJSON: `{"eventId":"auth-run:1","runId":"auth-run","seq":1,"type":"before","payload":{},"createdAt":"2026-11-22T00:00:00Z"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CloudAgentPiEntry{
		SessionID: session.ID, Sequence: 1, EntryID: "entry-before", UserID: userID,
		RunID: runID, EntryJSON: `{"role":"toolResult","content":"before"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(registerAuthNegativeRouter(svc))
	t.Cleanup(server.Close)
	before := readAuthSnapshot(t, db, runID, session.ID)

	invalid := sendAuthWrite(t, server, runID, owner, epoch, strings.Repeat("x", 48))
	if invalid.status != http.StatusUnauthorized {
		t.Fatalf("invalid credential status=%d, want 401; body=%s", invalid.status, invalid.body)
	}
	assertAuthSnapshotUnchanged(t, before, readAuthSnapshot(t, db, runID, session.ID))

	token, err := svc.IssueAgentRunGrant("auth-worker", owner, userID, runID, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentPiSession{}).
		Where("id = ?", session.ID).
		Updates(map[string]any{"lease_owner": "other-worker:slot", "lease_epoch": epoch + 1}).Error; err != nil {
		t.Fatal(err)
	}
	takenOver := readAuthSnapshot(t, db, runID, session.ID)
	stale := sendAuthWrite(t, server, runID, owner, epoch, token)
	if stale.status != http.StatusForbidden || stale.reason != "agent_lease_lost" {
		t.Fatalf("stale lease status=%d reason=%q, want 403 agent_lease_lost; body=%s", stale.status, stale.reason, stale.body)
	}
	assertAuthSnapshotUnchanged(t, takenOver, readAuthSnapshot(t, db, runID, session.ID))
}

type authNegativeResponse struct {
	status int
	reason string
	body   string
}

func registerAuthNegativeRouter(svc *service.Service) http.Handler {
	router := gin.New()
	RegisterInternalAgentRoutes(router, svc)
	return router
}

func sendAuthWrite(t *testing.T, server *httptest.Server, runID, owner string, epoch int64, token string) authNegativeResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/internal-agent/runs/"+runID+"/messages", strings.NewReader(`{"sequence":99,"message":{"role":"assistant","content":[{"type":"text","text":"must not write"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	identity, _, _ := strings.Cut(owner, ":")
	setWakeWorkerHeaders(request, identity, token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Agent-User-ID", "auth-user")
	request.Header.Set("X-Agent-Worker-ID", owner)
	request.Header.Set("X-Agent-Session-Epoch", fmt.Sprint(epoch))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &envelope)
	return authNegativeResponse{status: response.StatusCode, reason: envelope.Reason, body: string(body)}
}

type authSnapshot struct {
	Run        model.CloudAgentExecution
	Session    model.CloudAgentPiSession
	Transcript []model.CloudAgentMessageRecord
	Journal    []model.CloudAgentEventRecord
	Entries    []model.CloudAgentPiEntry
}

func readAuthSnapshot(t *testing.T, db *gorm.DB, runID, sessionID string) authSnapshot {
	t.Helper()
	var snapshot authSnapshot
	if err := db.Where("id = ?", runID).First(&snapshot.Run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", sessionID).First(&snapshot.Session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("run_id = ?", runID).Order("kind,sequence").Find(&snapshot.Transcript).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("run_id = ?", runID).Order("sequence").Find(&snapshot.Journal).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("session_id = ?", sessionID).Order("sequence").Find(&snapshot.Entries).Error; err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertAuthSnapshotUnchanged(t *testing.T, before, after authSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		t.Fatalf("rejected request changed persisted business state:\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
}
