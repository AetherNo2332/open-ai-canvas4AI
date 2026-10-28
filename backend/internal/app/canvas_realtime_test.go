package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/canvas"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/database"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func realtimeWriterFixture(t *testing.T, pg bool) (*repository.Repository, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "realtime.db") + "?_busy_timeout=5000&_journal_mode=WAL"
	var dialect gorm.Dialector = sqlite.Open(path)
	if pg {
		dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("isolated PostgreSQL DSN not configured")
		}
		admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		schema := fmt.Sprintf("canvas_realtime_%d", time.Now().UnixNano())
		if err = admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
				t.Error(err)
			}
			conn, _ := admin.DB()
			_ = conn.Close()
		})
		parsed, _ := url.Parse(dsn)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dialect = postgres.Open(parsed.String())
	}
	db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn, _ := db.DB(); _ = conn.Close() })
	if err = db.AutoMigrate(&model.User{}, &model.CanvasProject{}, &model.CanvasSnapshot{}, &model.CanvasShare{}, &model.CanvasUnitLink{}, &model.CanvasNodeEditLease{}, &model.CloudAgentExecution{}, &model.CloudAgentEventRecord{}, &model.CloudAgentMessageRecord{}, &model.Resource{}, &model.Asset{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	return repository.New(db), db
}
func nextRealtimeInvalidate(t *testing.T, s *canvas.RealtimeSession, want bool) canvas.RealtimeMessage {
	t.Helper()
	wait := 150 * time.Millisecond
	if want {
		wait = 2 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case raw := <-s.Messages():
			s.Consumed(raw)
			var msg canvas.RealtimeMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Type == "invalidate" {
				if !want {
					t.Fatalf("uncommitted/rolled-back write notified: %+v", msg)
				}
				return msg
			}
		case <-s.Done():
			t.Fatal("realtime session unexpectedly closed")
		case <-timer.C:
			if want {
				t.Fatal("committed write not observed")
			}
			return canvas.RealtimeMessage{}
		}
	}
}
func TestRealtimeObserverOnlySeesCommittedRootCommandAndAgentWrites(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			repo, db := realtimeWriterFixture(t, pg)
			p := identity.Principal{ID: "owner", OwnerID: "owner", Role: "owner", Scopes: identity.OwnerScopes()}
			service := commands.NewService(repo, commands.ServiceOptions{})
			snap, err := service.Create(context.Background(), p, commands.CreateCanvasRequest{ClientID: "client", OperationID: "create", InitialOperations: []commands.Operation{{Type: "add_node", ID: "text", NodeType: "text"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Create(&model.CloudAgentExecution{ID: "agent", UserID: "owner", Status: "running", StateJSON: "{}"}).Error; err != nil {
				t.Fatal(err)
			}
			hub := canvas.NewRealtimeHub(canvas.RealtimeOptions{ReadCommitted: repo.CanvasCommittedStates, ObserveInterval: 10 * time.Millisecond})
			defer hub.Close()
			session, err := hub.Join(context.Background(), canvas.RealtimeRoomKey{OwnerID: "owner", CanvasID: snap.CanvasID})
			if err != nil {
				t.Fatal(err)
			}
			nextRealtimeInvalidate(t, session, true)
			stop := errors.New("rollback actual root")
			for _, entry := range []string{"command", "agent"} {
				t.Run(entry, func(t *testing.T) {
					before, err := service.ReadSnapshot(context.Background(), p, snap.CanvasID)
					if err != nil {
						t.Fatal(err)
					}
					req := commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: before.DocumentEpoch, ClientID: "client", OperationID: entry, BaseRevision: before.Revision, Operations: []commands.Operation{{Type: "update_node", ID: "text", Patch: map[string]any{"content": entry}}}}
					write := func(tx *repository.Repository) error {
						if entry == "command" {
							_, err := commands.NewService(tx, commands.ServiceOptions{}).Execute(context.Background(), p, req)
							return err
						}
						run, _ := tx.CloudAgent("owner", "agent")
						return tx.MutateCloudAgent("owner", "agent", run.Revision, func(_ *model.CloudAgentExecution, writer *repository.Repository) error {
							row, err := writer.CanvasProjectForUser("owner", snap.CanvasID)
							if err != nil {
								return err
							}
							var doc map[string]any
							_ = json.Unmarshal([]byte(row.PayloadJSON), &doc)
							doc["nodes"].([]any)[0].(map[string]any)["title"] = entry
							raw, _ := json.Marshal(doc)
							row.PayloadJSON = string(raw)
							return writer.UpsertCanvasProject(row)
						})
					}
					err = repo.CommandTransaction(context.Background(), func(tx *repository.Repository) error {
						if err := write(tx); err != nil {
							return err
						}
						nextRealtimeInvalidate(t, session, false)
						return stop
					})
					if !errors.Is(err, stop) {
						t.Fatal(err)
					}
					nextRealtimeInvalidate(t, session, false)
					if err = repo.CommandTransaction(context.Background(), write); err != nil {
						t.Fatal(err)
					}
					notified := nextRealtimeInvalidate(t, session, true)
					if notified.EventSeq <= before.EventSeq || notified.DocumentEpoch != before.DocumentEpoch {
						t.Fatalf("wrong committed tuple: %+v", notified)
					}
				})
			}
			if err = repo.DeleteCanvasProject("owner", snap.CanvasID); err != nil {
				t.Fatal(err)
			}
			if !nextRealtimeInvalidate(t, session, true).Deleted {
				t.Fatal("physical delete was not observed")
			}
		})
	}
}

func TestRealtimeRedisOutageDoesNotBlockDurableWrites(t *testing.T) {
	repo, _ := realtimeWriterFixture(t, false)
	row := &model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: `{"nodes":[],"connections":[]}`}
	if err := repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 20 * time.Millisecond, ReadTimeout: 20 * time.Millisecond, WriteTimeout: 20 * time.Millisecond, MaxRetries: -1, ContextTimeoutEnabled: true})
	defer client.Close()
	hub := canvas.NewRealtimeHub(canvas.RealtimeOptions{ReadCommitted: repo.CanvasCommittedStates, Redis: client, Namespace: "canvas:unavailable-test", ObserveInterval: 10 * time.Millisecond, RedisHeartbeat: 50 * time.Millisecond})
	defer hub.Close()
	s, err := hub.Join(context.Background(), canvas.RealtimeRoomKey{OwnerID: "owner", CanvasID: "canvas"})
	if err != nil {
		t.Fatal(err)
	}
	nextRealtimeInvalidate(t, s, true)
	row.Title = "changed"
	row.PayloadJSON = `{"title":"changed","nodes":[],"connections":[]}`
	start := time.Now()
	if err = repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Redis outage delayed durable save")
	}
	nextRealtimeInvalidate(t, s, true)
}

func TestRealtimeServiceCloseWithoutRedisStopsLazyHub(t *testing.T) {
	s := &Service{}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenCanvasRealtime(context.Background(), "", "canvas"); err == nil {
		t.Fatal("empty closed service opened realtime")
	}
	hub := canvas.NewRealtimeHub(canvas.RealtimeOptions{})
	conn, err := hub.Join(context.Background(), canvas.RealtimeRoomKey{OwnerID: "owner", CanvasID: "canvas"})
	if err != nil {
		t.Fatal(err)
	}
	live := &Service{realtime: hub}
	if err = live.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("non-Redis Close leaked realtime session")
	}
}
