package worker

import (
	"strings"
	"testing"
	"time"
	"yingce/backend/internal/model"
)

type grantRepository struct {
	worker model.AgentWorker
	live   bool
}

func (r *grantRepository) AgentWorker(id string) (*model.AgentWorker, error) { return &r.worker, nil }
func (r *grantRepository) AgentRunAuthorization(user, run, owner string, epoch int64) bool {
	return r.live && user == "user" && run == "run" && owner == "worker:slot" && epoch == 1
}

func TestGrantBindingsRevocationAndLiveLease(t *testing.T) {
	repo := &grantRepository{worker: model.AgentWorker{ID: "worker", UserID: "user"}, live: true}
	service := NewGrants(repo, []byte(strings.Repeat("k", 48)))
	token, err := service.Issue("worker", "worker:slot", "user", "run", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !service.Validate(token, "user", "run", "worker:slot", 1) {
		t.Fatal("live grant rejected")
	}
	for _, bad := range []struct {
		user, run, owner string
		epoch            int64
	}{{"other", "run", "worker:slot", 1}, {"user", "other", "worker:slot", 1}, {"user", "run", "worker:other", 1}, {"user", "run", "worker:slot", 2}} {
		if service.Validate(token, bad.user, bad.run, bad.owner, bad.epoch) {
			t.Fatalf("binding escaped: %+v", bad)
		}
	}
	repo.live = false
	if service.Validate(token, "user", "run", "worker:slot", 1) {
		t.Fatal("terminal/takeover lease accepted")
	}
	repo.live = true
	now := time.Now()
	repo.worker.RevokedAt = &now
	if service.Validate(token, "user", "run", "worker:slot", 1) {
		t.Fatal("revoked grant accepted")
	}
	repo.worker.RevokedAt = nil
	repo.worker.UserID = "other"
	if service.Validate(token, "user", "run", "worker:slot", 1) {
		t.Fatal("other tenant worker accepted")
	}
}

func TestGrantRejectsMalformedAndExpired(t *testing.T) {
	repo := &grantRepository{worker: model.AgentWorker{ID: "worker"}, live: true}
	s := NewGrants(repo, []byte(strings.Repeat("k", 48)))
	token, err := s.Issue("worker", "worker:slot", "user", "run", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", token + "x", "run.bad.bad", "other"} {
		if s.Validate(bad, "user", "run", "worker:slot", 1) {
			t.Fatal("bad signature accepted")
		}
	}
	s.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	if s.Validate(token, "user", "run", "worker:slot", 1) {
		t.Fatal("expired grant accepted")
	}
	if _, err := NewGrants(repo, []byte("short")).Issue("worker", "worker:slot", "user", "run", 1); err == nil {
		t.Fatal("short key accepted")
	}
}
