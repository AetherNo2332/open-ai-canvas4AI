// Package agentservice is the business-neutral Agent service contract:
// definitions, fixed Session scope, single-writer leases and the ports the
// composition root implements. It holds no persistence or provider code.
package agentservice

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const (
	RoleSupervisor = "supervisor"
	RoleBusiness   = "business"

	ApplicationCanvas        = "canvas"
	ApplicationDrama         = "drama"
	ApplicationEcommerce     = "ecommerce"
	ApplicationOrchestration = "orchestration"

	SpaceCanvas         = "canvas"
	SpaceDramaProject   = "drama_project"
	SpaceWorkbenchBatch = "workbench_batch"
	SpaceWork           = "work"

	SessionActive   = "active"
	SessionArchived = "archived"
)

// Scope is fixed at Session creation. The owner is not part of it: it always
// comes from the trusted caller, so a request body can never name another user.
type Scope struct {
	Application string `json:"application"`
	SpaceType   string `json:"spaceType"`
	SpaceID     string `json:"spaceId"`
}

// Normalized trims every part of the scope.
func (s Scope) Normalized() Scope {
	return Scope{Application: strings.TrimSpace(s.Application), SpaceType: strings.TrimSpace(s.SpaceType), SpaceID: strings.TrimSpace(s.SpaceID)}
}

// Caller is the authenticated principal injected by the HTTP adapter or a
// trusted server-side flow.
type Caller struct {
	UserID string
}

type DefinitionView struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Role        string   `json:"role"`
	Application string   `json:"application"`
	SpaceTypes  []string `json:"spaceTypes"`
	Title       string   `json:"title"`
	Tools       []string `json:"tools"`
}

type SessionView struct {
	ID                string     `json:"id"`
	DefinitionID      string     `json:"definitionId"`
	DefinitionVersion string     `json:"definitionVersion"`
	Role              string     `json:"role"`
	Scope             Scope      `json:"scope"`
	WorkItemID        string     `json:"workItemId,omitempty"`
	ConversationID    string     `json:"conversationId,omitempty"`
	Title             string     `json:"title"`
	Status            string     `json:"status"`
	Revision          int64      `json:"revision"`
	ActiveRunID       string     `json:"activeRunId,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	ArchivedAt        *time.Time `json:"archivedAt,omitempty"`
}

type CreateSessionRequest struct {
	DefinitionID   string `json:"definitionId"`
	Scope          Scope  `json:"scope"`
	WorkItemID     string `json:"workItemId,omitempty"`
	Title          string `json:"title,omitempty"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// Fingerprint identifies the request content; the idempotency key is the
// lookup key and is excluded so that a replay with the same body matches.
func (r CreateSessionRequest) Fingerprint() string {
	scope := r.Scope.Normalized()
	parts := []string{strings.TrimSpace(r.DefinitionID), scope.Application, scope.SpaceType, scope.SpaceID, strings.TrimSpace(r.WorkItemID), strings.TrimSpace(r.Title)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

type ListSessionsRequest struct {
	Scope  *Scope `json:"scope,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type SessionPage struct {
	Sessions   []SessionView `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

type ArchiveSessionRequest struct {
	SessionID        string `json:"sessionId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
