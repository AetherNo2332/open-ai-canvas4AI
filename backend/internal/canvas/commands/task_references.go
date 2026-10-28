package commands

import (
	"context"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/repository"
)

// Task admission uses the same transaction and room resource policy as editor
// commands. This reads metadata only; it never downloads reference bytes.
func (s *Service) ValidateTaskReferences(ctx context.Context, tx *repository.Repository, p identity.Principal, doc Document, input map[string]any) (map[string]bool, error) {
	v, err := s.validation(ctx, tx, p, Document{"task": input}, nil)
	if err != nil {
		return nil, err
	}
	for _, id := range resourceIDs(Document{"task": input}) {
		if v.ResourceAllowed == nil || !v.ResourceAllowed(id) {
			return nil, failure("resource_not_owned", "任务引用包含未授权资源")
		}
	}
	reads := map[string]bool{}
	var missing bool
	walkNodeReferences(input, func(id string) {
		if id == "" {
			return
		}
		reads["node:"+id] = true
		if entity(doc, "node:"+id) == nil {
			missing = true
		}
	})
	if missing {
		return nil, failure("entity_deleted", "任务引用节点已删除")
	}
	return reads, nil
}
