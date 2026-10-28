package app

import (
	"context"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/canvas"
	"yingce/backend/internal/kernel"
)

type CanvasRealtimeSession = canvas.RealtimeSession

func (s *Service) AuthorizeCanvasRealtime(ctx context.Context, cookie, ownerID, canvasID string) error {
	if s == nil || s.realtimeBase == nil {
		return kernel.NewAppError(503, "实时画布尚未初始化")
	}
	repo := s.realtimeBase.WithContext(ctx)
	user, err := auth.New(repo, authHost{svc: s}, nil).CurrentUser(cookie)
	if err != nil {
		return err
	}
	if ownerID != "" && user.ID != ownerID {
		return kernel.Forbidden("实时画布账号已变化")
	}
	if _, err = repo.CanvasProjectMetadata(user.ID, canvasID); err != nil {
		if repo.IsMissing(err) {
			return kernel.NotFound("画布不存在或无权访问")
		}
		return err
	}
	epoch, err := repo.CommandEpoch(user.ID, canvasID)
	if err != nil && !repo.IsMissing(err) {
		return err
	}
	if err == nil && epoch.Deleted {
		return kernel.NotFound("画布已删除")
	}
	return nil
}
func (s *Service) OpenCanvasRealtime(ctx context.Context, cookie, canvasID string) (*CanvasRealtimeSession, error) {
	if err := s.AuthorizeCanvasRealtime(ctx, cookie, "", canvasID); err != nil {
		return nil, err
	}
	user, err := auth.New(s.realtimeBase.WithContext(ctx), authHost{svc: s}, nil).CurrentUser(cookie)
	if err != nil {
		return nil, err
	}
	s.realtimeMu.Lock()
	if s.realtimeClosed {
		s.realtimeMu.Unlock()
		return nil, kernel.NewAppError(503, "实时画布正在关闭")
	}
	if s.realtime == nil {
		options := canvas.RealtimeOptions{ReadCommitted: s.realtimeBase.CanvasCommittedStates}
		if s.coordinator != nil && s.coordinator.HasRedis() {
			options.Redis = s.coordinator.Redis()
		}
		s.realtime = canvas.NewRealtimeHub(options)
	}
	hub := s.realtime
	s.realtimeMu.Unlock()
	session, err := hub.Join(ctx, canvas.RealtimeRoomKey{OwnerID: user.ID, CanvasID: canvasID})
	if err != nil {
		return nil, kernel.NewAppError(503, "实时画布连接已达上限，请稍后重连")
	}
	return session, nil
}
func (s *Service) closeCanvasRealtime() error {
	if s == nil {
		return nil
	}
	s.realtimeMu.Lock()
	s.realtimeClosed = true
	hub := s.realtime
	s.realtimeMu.Unlock()
	if hub != nil {
		return hub.Close()
	}
	return nil
}
