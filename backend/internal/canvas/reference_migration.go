package canvas

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"yingce/backend/internal/model"

	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/kernel"
)

// CanvasReferenceMigrationPreview 告诉画布页旧版画布是否需要规范化，以及仍需用户选来源的作用域。
type CanvasReferenceMigrationPreview struct {
	Revision  int64                          `json:"revision"`
	Required  bool                           `json:"required"`
	Conflicts []references.MigrationConflict `json:"conflicts"`
}

// CanvasReferenceMigrationRequest 携带客户端看到的修订号与每个冲突作用域的来源选择。
type CanvasReferenceMigrationRequest struct {
	Revision *int64            `json:"revision"`
	Choices  map[string]string `json:"choices"`
}

// PreviewCanvasReferenceMigration 只读：不写文档、不分配标识。
func (s *Service) PreviewCanvasReferenceMigration(userID, canvasID string) (CanvasReferenceMigrationPreview, error) {
	project, err := s.ownedCanvasProject(userID, canvasID)
	if err != nil {
		return CanvasReferenceMigrationPreview{}, err
	}
	doc, err := canvasReferenceDocument(project.PayloadJSON)
	if err != nil {
		return CanvasReferenceMigrationPreview{}, err
	}
	preview := references.PreviewLegacy(doc, nil)
	return CanvasReferenceMigrationPreview{Revision: project.Revision, Required: preview.Required, Conflicts: preview.Conflicts}, nil
}

// MigrateCanvasReferences 把旧版画布一次性切换到声明式引用。它走与资源修复相同的整份保存：
// 修订号 CAS、保存前留一份历史快照（可在画布历史中恢复）、编辑租约与投影，其他标签页经
// 画布变更通知拿到新版本。冲突未选完时不写入。
func (s *Service) MigrateCanvasReferences(userID, canvasID string, req CanvasReferenceMigrationRequest) (UserDataSummary, error) {
	if req.Revision == nil {
		return UserDataSummary{}, kernel.NewAppError(http.StatusPreconditionRequired, "缺少画布版本，请重新加载画布")
	}
	for key, choice := range req.Choices {
		if key == "" || len(key) > 400 || (choice != references.MigrationChoiceFields && choice != references.MigrationChoiceConnections) {
			return UserDataSummary{}, kernel.BadAuthRequest("引用来源只能选 fields 或 connections")
		}
	}
	project, err := s.ownedCanvasProject(userID, canvasID)
	if err != nil {
		return UserDataSummary{}, err
	}
	if project.Revision != *req.Revision {
		return UserDataSummary{}, canvasRevisionConflict()
	}
	doc, err := canvasReferenceDocument(project.PayloadJSON)
	if err != nil {
		return UserDataSummary{}, err
	}
	if references.Enabled(doc) {
		return UserDataSummary{}, canvasMutationError("reference_migration_not_required", "画布已经使用声明式引用，无需再次规范化")
	}
	migrated, err := references.NormalizeLegacy(doc, req.Choices, nil, nil)
	if err != nil {
		if typed, ok := err.(*references.Error); ok {
			appErr := kernel.BadAuthRequest(typed.Message)
			appErr.Reason = "reference_migration_invalid"
			return UserDataSummary{}, appErr
		}
		return UserDataSummary{}, err
	}
	if len(migrated.Conflicts) > 0 {
		return UserDataSummary{}, canvasMutationError("reference_migration_conflict", "仍有引用来源未选择，请为每个冲突选择以字段或连线为准")
	}
	migrated.Document["id"] = canvasID
	migrated.Document["revision"] = project.Revision
	raw, err := json.Marshal(migrated.Document)
	if err != nil {
		return UserDataSummary{}, err
	}
	return s.upsertUserCanvasProjectWithHistory(userID, raw, "before_reference_migration")
}

func canvasReferenceDocument(payload string) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return nil, kernel.BadAuthRequest("画布内容格式错误")
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// ownedCanvasProject 读取当前用户自己的画布（含正文）；不存在或不属于该用户一律 404。
func (s *Service) ownedCanvasProject(userID, canvasID string) (*model.CanvasProject, error) {
	project, err := s.repo.CanvasProjectForUser(userID, strings.TrimSpace(canvasID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.NewAppError(http.StatusNotFound, "画布不存在或无权访问")
	}
	return project, err
}
