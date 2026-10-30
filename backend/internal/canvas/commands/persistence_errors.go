package commands

import (
	"errors"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/repository"
)

// ErrRevisionConflict is a stable protocol boundary alias; handlers do not
// depend on persistence packages to classify a failed final document CAS.
var ErrRevisionConflict = repository.ErrCanvasRevisionConflict

func PersistenceError(err error) error {
	if errors.Is(err, repository.ErrCanvasMediaVersionManaged) {
		return failure("media_version_managed", err.Error())
	}
	var version *mediaversions.Error
	if errors.As(err, &version) {
		return &Error{Reason: version.Reason, Message: version.Message}
	}
	if errors.Is(err, ErrRevisionConflict) {
		return failure("revision_conflict", "画布已变化，请读取最新版本后重试")
	}
	var lease *repository.CanvasEditLeaseError
	if errors.As(err, &lease) {
		if lease.Reason == "node_locked" {
			return &Error{Reason: lease.Reason, Message: "用户正在编辑该节点（nodeId=" + lease.NodeID + "），请等待编辑结束后重试", Field: lease.NodeID}
		}
		return &Error{Reason: lease.Reason, Message: "编辑租约已变化，请保留草稿并重新获取租约", Field: lease.NodeID}
	}
	return err
}
