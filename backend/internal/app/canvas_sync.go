package app

import (
	"context"
	"infinite-canvas/backend/internal/canvas"
)

type CanvasEditProof = canvas.CanvasEditProof
type CanvasEditLeaseGrant = canvas.CanvasEditLeaseGrant
type CanvasSyncStatus = canvas.CanvasSyncStatus

// CanvasRequest creates only a request-scoped domain facade. Service contains
// mutexes and long-lived runtimes and must never be copied for request state.
func (s *Service) CanvasRequest(ctx context.Context) *canvas.Service {
	return canvas.New(s.repo.WithContext(ctx), canvasHost{svc: s})
}
func (s *Service) AcquireCanvasEditLease(ctx context.Context, owner, canvasID string, proof CanvasEditProof) (CanvasEditLeaseGrant, error) {
	return s.canvasDomain().AcquireCanvasEditLease(ctx, owner, canvasID, proof)
}
func (s *Service) RenewCanvasEditLease(ctx context.Context, owner, canvasID string, proof CanvasEditProof) (CanvasEditLeaseGrant, error) {
	return s.canvasDomain().RenewCanvasEditLease(ctx, owner, canvasID, proof)
}
func (s *Service) ReleaseCanvasEditLease(ctx context.Context, owner, canvasID string, proof CanvasEditProof) error {
	return s.canvasDomain().ReleaseCanvasEditLease(ctx, owner, canvasID, proof)
}
func (s *Service) CanvasSyncStatus(ctx context.Context, owner, canvasID string) (CanvasSyncStatus, error) {
	return s.canvasDomain().CanvasSyncStatus(ctx, owner, canvasID)
}
