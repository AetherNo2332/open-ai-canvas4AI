package agentservice

import "context"

// Sessions is the Session group of the Agent service.
type Sessions interface {
	Definitions(ctx context.Context, caller Caller, application string) ([]DefinitionView, error)
	Create(ctx context.Context, caller Caller, req CreateSessionRequest) (SessionView, error)
	Get(ctx context.Context, caller Caller, sessionID string) (SessionView, error)
	List(ctx context.Context, caller Caller, req ListSessionsRequest) (SessionPage, error)
	Archive(ctx context.Context, caller Caller, req ArchiveSessionRequest) (SessionView, error)
}

// SpaceAdapter verifies that a business space exists and belongs to the owner.
// Each application registers one; the Session core never reads business tables.
type SpaceAdapter interface {
	ValidateSpace(ctx context.Context, ownerID string, scope Scope) error
}
