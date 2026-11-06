package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/workbench"
)

const (
	agentSessionLeaseTTL     = 2 * time.Minute
	agentSessionPageDefault  = 50
	agentSessionPageMax      = 100
	agentSessionMappingBatch = 200
	canvasAgentDefinitionID  = "canvas.agent"
)

// agentSessionHost implements the Session group over the composition root.
type agentSessionHost struct{ s *Service }

func (s *Service) AgentSessions() as.Sessions { return agentSessionHost{s: s} }

// agentRegistry is the server-side definition registry. Definitions ship with
// the code; nothing at runtime can add one or widen its tools.
func (s *Service) agentRegistry() *as.Registry {
	s.agentServiceOnce.Do(func() {
		registry := as.NewRegistry()
		for _, def := range []as.Definition{
			{ID: canvasAgentDefinitionID, Version: "1", Role: as.RoleBusiness, Application: as.ApplicationCanvas, SpaceTypes: []string{as.SpaceCanvas}, Title: "画布 Agent"},
			{ID: "drama.agent", Version: "1", Role: as.RoleBusiness, Application: as.ApplicationDrama, SpaceTypes: []string{as.SpaceDramaProject}, Title: "短剧 Agent"},
			{ID: "ecommerce.agent", Version: "1", Role: as.RoleBusiness, Application: as.ApplicationEcommerce, SpaceTypes: []string{as.SpaceWorkbenchBatch}, Title: "电商 Agent"},
			{ID: supervisorDefinitionID, Version: "1", Role: as.RoleSupervisor, Application: as.ApplicationOrchestration, SpaceTypes: []string{as.SpaceWork}, Title: "总控 Agent"},
		} {
			if err := registry.Register(def); err != nil {
				panic(err)
			}
		}
		s.agentDefinitions = registry
	})
	return s.agentDefinitions
}

// validateAgentSpace checks that the space exists and belongs to the owner.
// Built-in applications use the rules below; registered businesses use their
// adapter. The Session core never reads business rows.
func (s *Service) validateAgentSpace(ownerID string, scope as.Scope) error {
	switch scope.Application {
	case as.ApplicationCanvas, as.ApplicationDrama, as.ApplicationEcommerce:
		return s.validateBuiltinAgentSpace(ownerID, scope)
	}
	if adapter, ok := s.agentAdapters().Adapter(scope.Application); ok {
		return adapter.ValidateSpace(context.Background(), ownerID, scope)
	}
	return as.NewError(as.CodeScopeMismatch, "该业务尚未接入 Agent 服务")
}

func (s *Service) validateBuiltinAgentSpace(ownerID string, scope as.Scope) error {
	switch scope.Application {
	case as.ApplicationCanvas:
		_, err := s.repo.CanvasProjectForUser(ownerID, scope.SpaceID)
		return agentSpaceError(err, "画布不存在或不属于当前账号")
	case as.ApplicationDrama:
		project, err := s.repo.ProjectForUser(ownerID, scope.SpaceID)
		if err == nil && project.Type != "short-drama" {
			err = gorm.ErrRecordNotFound
		}
		return agentSpaceError(err, "短剧项目不存在或不属于当前账号")
	case as.ApplicationEcommerce:
		// Workbench batches are client-generated IDs with no table of their own;
		// the owner scope of the Session is the ownership boundary.
		if !workbench.ValidTaskBatch(scope.SpaceID, 0, 1) {
			return as.NewError(as.CodeInvalidRequest, "电商批次 ID 格式无效")
		}
		return nil
	}
	return as.NewError(as.CodeScopeMismatch, "该业务尚未接入 Agent 服务")
}

func agentSpaceError(err error, notFound string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return as.NewError(as.CodeNotFound, notFound)
	}
	return err
}

func agentSessionID(ownerID, key string) string {
	sum := sha256.Sum256([]byte(ownerID + "\x00" + key))
	return "as" + hex.EncodeToString(sum[:16])
}

func agentCaller(caller as.Caller) (string, error) {
	userID := strings.TrimSpace(caller.UserID)
	if userID == "" {
		return "", as.NewError(as.CodeUnauthorized, "请先登录")
	}
	return userID, nil
}

func (h agentSessionHost) Definitions(_ context.Context, caller as.Caller, application string) ([]as.DefinitionView, error) {
	if _, err := agentCaller(caller); err != nil {
		return nil, err
	}
	return h.s.agentRegistry().List(application), nil
}

func (h agentSessionHost) Create(_ context.Context, caller as.Caller, req as.CreateSessionRequest) (as.SessionView, error) {
	ownerID, err := agentCaller(caller)
	if err != nil {
		return as.SessionView{}, err
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" || len(key) > 160 || utf8.RuneCountInString(req.Title) > 240 {
		return as.SessionView{}, as.NewError(as.CodeInvalidRequest, "幂等键必填（最长 160），标题最长 240 字")
	}
	if strings.TrimSpace(req.WorkItemID) != "" {
		return as.SessionView{}, as.NewError(as.CodeInvalidRequest, "工作单绑定尚未开放")
	}
	scope := req.Scope.Normalized()
	def, err := h.s.agentRegistry().Resolve(req.DefinitionID, scope)
	if err != nil {
		return as.SessionView{}, err
	}
	if err := h.s.validateAgentSpace(ownerID, scope); err != nil {
		return as.SessionView{}, err
	}
	// API keys live in their own namespace so they can never collide with the
	// deterministic keys of migrated canvas conversations.
	stored := "api:" + key
	row := &model.AgentSession{
		ID: agentSessionID(ownerID, stored), OwnerID: ownerID, IdempotencyKey: stored, Fingerprint: req.Fingerprint(),
		DefinitionID: def.ID, DefinitionVersion: def.Version, Role: def.Role,
		Application: scope.Application, SpaceType: scope.SpaceType, SpaceID: scope.SpaceID,
		Title: firstNonEmpty(strings.TrimSpace(req.Title), def.Title), Status: as.SessionActive, Revision: 1,
	}
	session, created, err := h.s.repo.CreateAgentSession(row)
	if err != nil {
		return as.SessionView{}, err
	}
	if !created && session.Fingerprint != row.Fingerprint {
		return as.SessionView{}, as.NewError(as.CodeIdempotencyConflict, "幂等键已用于不同的会话请求")
	}
	return agentSessionView(*session), nil
}

func (h agentSessionHost) Get(_ context.Context, caller as.Caller, sessionID string) (as.SessionView, error) {
	ownerID, err := agentCaller(caller)
	if err != nil {
		return as.SessionView{}, err
	}
	session, err := h.s.agentSessionForOwner(ownerID, sessionID)
	if err != nil {
		return as.SessionView{}, err
	}
	return agentSessionView(*session), nil
}

func (h agentSessionHost) List(_ context.Context, caller as.Caller, req as.ListSessionsRequest) (as.SessionPage, error) {
	ownerID, err := agentCaller(caller)
	if err != nil {
		return as.SessionPage{}, err
	}
	var filter *repository.AgentSessionFilter
	if req.Scope != nil {
		scope := req.Scope.Normalized()
		if scope.Application == "" || scope.SpaceType == "" || scope.SpaceID == "" {
			return as.SessionPage{}, as.NewError(as.CodeInvalidRequest, "按范围筛选需要完整的业务、空间类型与空间 ID")
		}
		filter = &repository.AgentSessionFilter{Application: scope.Application, SpaceType: scope.SpaceType, SpaceID: scope.SpaceID}
	}
	if filter == nil || filter.Application == as.ApplicationCanvas {
		if err := h.s.ensureCanvasAgentSessions(ownerID); err != nil {
			return as.SessionPage{}, err
		}
	}
	before, err := decodeAgentSessionCursor(req.Cursor)
	if err != nil {
		return as.SessionPage{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = agentSessionPageDefault
	}
	limit = min(limit, agentSessionPageMax)
	rows, err := h.s.repo.AgentSessions(ownerID, filter, before, limit+1)
	if err != nil {
		return as.SessionPage{}, err
	}
	page := as.SessionPage{Sessions: []as.SessionView{}}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.NextCursor = encodeAgentSessionCursor(repository.AgentSessionCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	for _, row := range rows {
		page.Sessions = append(page.Sessions, agentSessionView(row))
	}
	return page, nil
}

func (h agentSessionHost) Archive(_ context.Context, caller as.Caller, req as.ArchiveSessionRequest) (as.SessionView, error) {
	ownerID, err := agentCaller(caller)
	if err != nil {
		return as.SessionView{}, err
	}
	session, err := h.s.agentSessionForOwner(ownerID, req.SessionID)
	if err != nil {
		return as.SessionView{}, err
	}
	now := time.Now()
	if err := h.s.repo.ArchiveAgentSession(ownerID, session.ID, req.ExpectedRevision, session.LeaseEpoch, now); err != nil {
		if errors.Is(err, repository.ErrAgentSessionChanged) {
			return as.SessionView{}, as.NewError(as.CodeSessionConflict, "会话已更新或正在处理请求，请刷新后再归档")
		}
		return as.SessionView{}, err
	}
	archived, err := h.s.agentSessionForOwner(ownerID, session.ID)
	if err != nil {
		return as.SessionView{}, err
	}
	return agentSessionView(*archived), nil
}

func (s *Service) agentSessionForOwner(ownerID, sessionID string) (*model.AgentSession, error) {
	session, err := s.repo.AgentSessionForOwner(ownerID, strings.TrimSpace(sessionID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, as.NewError(as.CodeNotFound, "会话不存在")
	}
	return session, err
}

func agentSessionView(row model.AgentSession) as.SessionView {
	return as.SessionView{
		ID: row.ID, DefinitionID: row.DefinitionID, DefinitionVersion: row.DefinitionVersion, Role: row.Role,
		Scope:      as.Scope{Application: row.Application, SpaceType: row.SpaceType, SpaceID: row.SpaceID},
		WorkItemID: row.WorkItemID, ConversationID: row.ConversationID, Title: row.Title, Status: row.Status,
		Revision: row.Revision, ActiveRunID: row.ActiveRunID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt,
	}
}

func encodeAgentSessionCursor(cursor repository.AgentSessionCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeAgentSessionCursor(value string) (*repository.AgentSessionCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor repository.AgentSessionCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.ID == "" {
		return nil, as.NewError(as.CodeInvalidRequest, "分页游标无效")
	}
	return &cursor, nil
}
