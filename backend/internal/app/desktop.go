package app

import (
	"context"
	"errors"
	"infinite-canvas/backend/internal/repository"
)

type SecretProvider interface{ MasterKey() ([]byte, error) }

func NewDesktop(repo *repository.Repository, dataDir string, provider SecretProvider) (*Service, error) {
	if provider == nil {
		return nil, errors.New("desktop protected secret provider required")
	}
	key, err := provider.MasterKey()
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("desktop master key must be 32 bytes")
	}
	svc := newService(repo, dataDir)
	svc.desktopMode = true
	svc.desktopMasterKey = append([]byte(nil), key...)
	if svc.runtimeErr != nil {
		return nil, svc.runtimeErr
	}
	if err := svc.loadDesktopPrivateHosts(); err != nil {
		return nil, err
	}
	return svc, nil
}

func (s *Service) StartDesktopWorkers(ctx context.Context) error {
	if !s.desktopMode {
		return errors.New("desktop worker entry requires desktop service")
	}
	workerCtx, started := s.backgroundWorkers().Start()
	if !started {
		return nil
	}
	s.taskWorker().start(workerCtx)
	s.startCanvasMaterializer()
	s.startResourceDeletionWorker(workerCtx)
	s.startSkillSyncWorker(workerCtx)
	return nil
}
