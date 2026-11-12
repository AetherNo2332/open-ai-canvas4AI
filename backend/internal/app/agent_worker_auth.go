package app

import (
	"os"
	"yingce/backend/internal/identity/worker"
	"yingce/backend/internal/model"
)

type AgentRunGrant = worker.Grant

func agentGrantKey() []byte                         { return []byte(os.Getenv("CANVAS_AGENT_INTERNAL_TOKEN")) }
func (s *Service) workerGrants() *worker.Grants     { return worker.NewGrants(s.repo, agentGrantKey()) }
func (s *Service) workerRegistry() *worker.Registry { return worker.NewRegistry(s.repo) }
func (s *Service) AgentWorkerOwnsOwner(workerID, owner string) bool {
	return worker.OwnsOwner(workerID, owner)
}
func (s *Service) SeedAgentWorker() error {
	return s.workerRegistry().Seed(os.Getenv("CANVAS_AGENT_WORKER_ID"), os.Getenv("CANVAS_AGENT_WORKER_TOKEN"), os.Getenv("CANVAS_AGENT_WORKER_USER_ID"))
}
func (s *Service) AuthenticateAgentWorker(id, secret string) (*model.AgentWorker, error) {
	return s.workerRegistry().Authenticate(id, secret)
}
func (s *Service) IssueAgentRunGrant(workerID, owner, userID, runID string, epoch int64) (string, error) {
	return s.workerGrants().Issue(workerID, owner, userID, runID, epoch)
}
func (s *Service) ValidateAgentRunGrant(token, userID, runID, owner string, epoch int64) bool {
	return s.workerGrants().Validate(token, userID, runID, owner, epoch)
}
func (s *Service) AgentRunGrantWorker(token string) (string, error) { return worker.GrantWorker(token) }
func (s *Service) AdminProvisionAgentWorker(actor *model.User, id, userID string) (string, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return "", err
	}
	if !worker.ValidID(id) {
		return "", BadAuthRequest("invalid worker ID")
	}
	return s.workerRegistry().Provision(id, userID)
}
func (s *Service) AdminRevokeAgentWorker(actor *model.User, id string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	return s.workerRegistry().Revoke(id)
}
func (s *Service) AdminAgentWorkers(actor *model.User) ([]model.AgentWorker, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	return s.workerRegistry().List()
}
