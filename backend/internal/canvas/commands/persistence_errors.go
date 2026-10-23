package commands

import (
	"errors"
	"infinite-canvas/backend/internal/repository"
)

// ErrRevisionConflict is a stable protocol boundary alias; handlers do not
// depend on persistence packages to classify a failed final document CAS.
var ErrRevisionConflict = repository.ErrCanvasRevisionConflict

func PersistenceError(err error) error {
	if errors.Is(err, ErrRevisionConflict) {
		return failure("revision_conflict", "画布已变化，请读取最新版本后重试")
	}
	var lease *repository.CanvasEditLeaseError
	if errors.As(err, &lease) {
		return &Error{Reason: lease.Reason, Message: "编辑租约已变化，请保留草稿并重新获取租约", Field: lease.NodeID}
	}
	return err
}
