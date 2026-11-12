package worker

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"gorm.io/gorm"
	"regexp"
	"strings"
	"yingce/backend/internal/model"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var suffixPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`)

func ValidID(id string) bool { return idPattern.MatchString(id) }
func OwnsOwner(workerID, owner string) bool {
	if !ValidID(workerID) || len(owner) > 80 || !strings.HasPrefix(owner, workerID+":") {
		return false
	}
	return suffixPattern.MatchString(strings.TrimPrefix(owner, workerID+":"))
}

type RegistryRepository interface {
	SeedAgentWorker(string, string, string) error
	AuthenticateAgentWorker(string, string) (*model.AgentWorker, error)
	ProvisionAgentWorker(string, string, string) error
	RevokeAgentWorker(string) error
	ListAgentWorkers() ([]model.AgentWorker, error)
}
type Registry struct{ repo RegistryRepository }

func NewRegistry(repo RegistryRepository) *Registry { return &Registry{repo: repo} }

func (r *Registry) Seed(id, secret, userID string) error {
	if id == "" && secret == "" {
		return nil
	}
	if !ValidID(id) || len(secret) < 32 {
		return errors.New("invalid worker bootstrap credential")
	}
	return r.repo.SeedAgentWorker(id, secret, userID)
}
func (r *Registry) Authenticate(id, secret string) (*model.AgentWorker, error) {
	if !ValidID(id) || secret == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return r.repo.AuthenticateAgentWorker(id, secret)
}

// Management callers must authorize the actor before entering these methods.
func (r *Registry) Provision(id, userID string) (string, error) {
	if !ValidID(id) {
		return "", errors.New("invalid worker ID")
	}
	secret := make([]byte, 48)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	credential := base64.RawURLEncoding.EncodeToString(secret)
	if err := r.repo.ProvisionAgentWorker(id, credential, userID); err != nil {
		return "", err
	}
	return credential, nil
}
func (r *Registry) Revoke(id string) error             { return r.repo.RevokeAgentWorker(id) }
func (r *Registry) List() ([]model.AgentWorker, error) { return r.repo.ListAgentWorkers() }
