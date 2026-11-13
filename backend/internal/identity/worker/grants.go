package worker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

type Grant struct {
	WorkerID string `json:"workerId"`
	Owner    string `json:"owner"`
	UserID   string `json:"userId"`
	RunID    string `json:"runId"`
	Epoch    int64  `json:"epoch"`
	Expires  int64  `json:"expires"`
}

type GrantRepository interface {
	AgentWorker(string) (*model.AgentWorker, error)
	AgentRunAuthorization(string, string, string, int64) bool
}

type Grants struct {
	repo GrantRepository
	key  []byte
	now  func() time.Time
}

// Signing material is injected by the control plane; this package never reads
// environment variables and never receives the application Service.
func NewGrants(repo GrantRepository, key []byte) *Grants {
	return &Grants{repo: repo, key: append([]byte(nil), key...), now: time.Now}
}

func (s *Grants) Issue(workerID, owner, userID, runID string, epoch int64) (string, error) {
	if len(s.key) < 32 || epoch <= 0 || !OwnsOwner(workerID, owner) || !s.repo.AgentRunAuthorization(userID, runID, owner, epoch) {
		return "", errors.New("invalid agent run lease")
	}
	worker, err := s.repo.AgentWorker(workerID)
	if err != nil || worker.RevokedAt != nil || (worker.UserID != "" && worker.UserID != userID) {
		return "", errors.New("invalid agent worker")
	}
	grant := Grant{WorkerID: workerID, Owner: owner, UserID: userID, RunID: runID, Epoch: epoch, Expires: s.now().UTC().Add(5 * time.Minute).Unix()}
	body, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return "run." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Grants) Validate(token, userID, runID, owner string, epoch int64) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "run" || len(s.key) < 32 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(body) > 2048 {
		return false
	}
	var grant Grant
	if json.Unmarshal(body, &grant) != nil || grant.Expires <= s.now().UTC().Unix() || grant.Expires > s.now().UTC().Add(5*time.Minute).Unix() || grant.UserID != userID || grant.RunID != runID || grant.Owner != owner || grant.Epoch != epoch {
		return false
	}
	worker, err := s.repo.AgentWorker(grant.WorkerID)
	if err != nil || worker.RevokedAt != nil || (worker.UserID != "" && worker.UserID != userID) || !OwnsOwner(grant.WorkerID, grant.Owner) {
		return false
	}
	return s.repo.AgentRunAuthorization(userID, runID, owner, epoch)
}

// GrantWorker only decodes the identity after the same request has passed
// Validate. It is not an authentication API.
func GrantWorker(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("invalid run authorization")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var grant Grant
	if err = json.Unmarshal(body, &grant); err != nil {
		return "", err
	}
	return grant.WorkerID, nil
}
