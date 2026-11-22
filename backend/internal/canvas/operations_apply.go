package canvas

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

// 回执只保存重放需要的摘要。每次 operations 都会写一条回执，写入整份画布会让表随编辑次数线性膨胀；
// 早期回执里残留的 document 字段在解码时被忽略，并随保留期过期清理。
type canvasMutationReceiptResult struct {
	Summary UserDataSummary `json:"summary"`
}

func decodeCanvasMutationReceipt(raw string, result *UserDataSummary) error {
	var stored canvasMutationReceiptResult
	if err := json.Unmarshal([]byte(raw), &stored); err == nil && stored.Summary.ID != "" {
		*result = stored.Summary
		return nil
	}
	return json.Unmarshal([]byte(raw), result)
}

func validateCanvasOperationsRequest(req CanvasOperationsRequest) error {
	if req.MutationID == "" || len(req.MutationID) > 160 || len(req.Operations) == 0 || len(req.Operations) > 500 {
		return kernel.BadAuthRequest("画布操作请求 ID、操作内容或批次大小无效")
	}
	if req.BaseRevision != nil && (*req.BaseRevision < 0 || *req.BaseRevision >= 9007199254740991) {
		return kernel.BadAuthRequest("画布基线版本无效")
	}
	if len(req.EditProofs) > 500 {
		return kernel.BadAuthRequest("画布编辑凭据数量超出限制")
	}
	if err := validateCanvasClientCapabilities(req.ClientCapabilities); err != nil {
		return err
	}
	seenProofs := make(map[string]bool, len(req.EditProofs))
	for _, proof := range req.EditProofs {
		if proof.NodeID == "" || len(proof.NodeID) > 160 || proof.SessionID == "" || len(proof.SessionID) > 160 || proof.Token == "" || len(proof.Token) > 80 || seenProofs[proof.NodeID] {
			return kernel.BadAuthRequest("画布编辑凭据无效或重复")
		}
		seenProofs[proof.NodeID] = true
	}
	return nil
}

// ApplyUserCanvasOperations applies only the objects changed by the caller to the
// latest server document. The request is idempotent by mutation ID and commits
// the canvas row plus its receipt in one database transaction.
func (s *Service) ApplyUserCanvasOperations(userID, canvasID string, req CanvasOperationsRequest) (UserDataSummary, error) {
	if canvasID == "" || len(canvasID) > 80 {
		return UserDataSummary{}, kernel.BadAuthRequest("画布操作请求 ID、操作内容或批次大小无效")
	}
	if err := validateCanvasOperationsRequest(req); err != nil {
		return UserDataSummary{}, err
	}
	quota, err := s.host.CanvasQuotaCheck(userID)
	if err != nil {
		return UserDataSummary{}, err
	}
	digest := CanvasOperationsDigest(req)
	var result UserDataSummary
	err = s.host.WithStorageLock(func() error {
		return s.repo.Transaction(func(tx *repository.Repository) error {
			return s.applyCanvasOperationsInTransaction(tx, userID, canvasID, req, digest, quota, &result)
		})
	})
	if err == nil {
		return result, nil
	}
	switch {
	case errors.Is(err, ErrCanvasObjectMissing):
		return UserDataSummary{}, canvasMutationError("canvas_object_missing", err.Error())
	case errors.Is(err, ErrCanvasOperationConflict):
		return UserDataSummary{}, canvasMutationError("canvas_field_conflict", err.Error())
	case errors.Is(err, repository.ErrCanvasRevisionConflict):
		retry := canvasMutationError("canvas_concurrent_write", "画布正在更新，将自动重放原请求")
		retry.Retryable = true
		return UserDataSummary{}, retry
	case errors.Is(err, repository.ErrCanvasEditLease):
		return UserDataSummary{}, canvasMutationError("canvas_edit_lease_conflict", err.Error())
	default:
		return UserDataSummary{}, err
	}
}

func (s *Service) applyCanvasOperationsInTransaction(tx *repository.Repository, userID, canvasID string, req CanvasOperationsRequest, digest string, quota func(*repository.Repository, bool, int64) error, result *UserDataSummary) error {
	tx = tx.WithCanvasEditProofs(req.EditProofs)
	if err := tx.LockCanvasMutation(userID, canvasID); err != nil {
		return err
	}
	candidate := &model.CanvasMutationReceipt{ID: kernel.NewID(), UserID: userID, CanvasID: canvasID, MutationID: req.MutationID, PayloadDigest: digest}
	created, err := tx.TryCreateCanvasMutationReceipt(candidate)
	if err != nil {
		return err
	}
	if !created {
		receipt, err := tx.CanvasMutationReceiptForUser(userID, canvasID, req.MutationID)
		if err != nil {
			return err
		}
		if receipt.PayloadDigest != digest {
			return canvasMutationError("canvas_mutation_id_reused", "同一画布请求 ID 对应的操作内容不一致")
		}
		return decodeCanvasMutationReceipt(receipt.ResultJSON, result)
	}
	existing, err := tx.CanvasProjectForUser(userID, canvasID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		existing = nil
	}
	now := time.Now().UTC()
	raw, err := prepareCanvasOperationDocument(tx, userID, canvasID, existing, req, now)
	if err != nil {
		return err
	}
	project, err := canvasProjectFromJSON(userID, raw)
	if err != nil {
		return err
	}
	if project.ID != canvasID {
		return kernel.BadAuthRequest("画布 ID 与请求路径不一致")
	}
	if existing != nil {
		project.ID, project.CreatedAt, project.Revision, project.UpdatedAt = existing.ID, existing.CreatedAt, existing.Revision, now
	} else {
		project.CreatedAt, project.Revision, project.UpdatedAt = now, 0, now
	}
	existingPayload := ""
	if existing != nil {
		existingPayload = existing.PayloadJSON
	}
	if err := quota(tx, existing == nil, int64(len(raw)-len(existingPayload))); err != nil {
		return err
	}
	if err := tx.UpsertCanvasProject(&project); err != nil {
		return err
	}
	*result = UserDataSummary{ID: project.ID, Title: project.Title, CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt, Revision: project.Revision,
		SaveAudit: &CanvasSaveAudit{NodesBefore: canvasNodeCount(existingPayload), NodesAfter: canvasNodeCount(project.PayloadJSON)}}
	encoded, err := json.Marshal(canvasMutationReceiptResult{Summary: *result})
	if err != nil {
		return err
	}
	candidate.ResultJSON, candidate.Revision = string(encoded), project.Revision
	return tx.FinalizeCanvasMutationReceipt(candidate)
}

func prepareCanvasOperationDocument(tx *repository.Repository, userID, canvasID string, existing *model.CanvasProject, req CanvasOperationsRequest, now time.Time) ([]byte, error) {
	payload, operations, err := canvasOperationsPayload(canvasID, existing, req)
	if err != nil {
		return nil, err
	}
	// Existing field-reference documents must never interpret connection operations.
	// A create request is different: it may carry legacy nodes and connections that
	// adoptDeclarativeReferences normalizes before the document format is validated.
	if existing != nil {
		if err := validateCanvasDocumentOperationFormat(payload, operations, req); err != nil {
			return nil, err
		}
	}
	explicitReferenceFormat := existing == nil && canvasDocumentUsesFieldReferences(payload)
	legacyConnectionWrites := hasCanvasConnectionOperations(operations)
	if err := applyCanvasOperations(payload, operations); err != nil {
		if errors.Is(err, ErrCanvasObjectMissing) || errors.Is(err, ErrCanvasOperationConflict) {
			return nil, err
		}
		var appError *kernel.AppError
		if errors.As(err, &appError) {
			return nil, err
		}
		return nil, kernel.BadAuthRequest(err.Error())
	}
	adoptedReferenceFormat := false
	if existing == nil {
		if canvasClientSupportsReferenceFields(req) {
			// ApplyUserCanvasOperations 每批最多 500 个操作；满批说明客户端拆分了创建。
			adopted, err := adoptDeclarativeReferences(payload, len(req.Operations) >= 500)
			if err != nil {
				return nil, err
			}
			adoptedReferenceFormat = adopted
		}
	}
	if explicitReferenceFormat && legacyConnectionWrites && !adoptedReferenceFormat {
		return nil, canvasMutationError("canvas_document_format_conflict", "旧引用无法规范化为字段引用格式，画布未保存")
	}
	if err := validateCanvasDocumentOperationFormat(payload, nil, req); err != nil {
		return nil, err
	}
	delete(payload, "revision")
	delete(payload, "remoteContentHash")
	payload["id"] = jsonRawValue(canvasID)
	payload["updatedAt"] = jsonRawValue(now)
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		raw, err = New(tx, nil).repairPurgedCanvasMediaReferences(userID, json.RawMessage(existing.PayloadJSON), raw)
		if err != nil {
			return nil, err
		}
	}
	if err := New(tx, nil).ValidateCanvasMediaAssets(userID, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func canvasMutationError(reason kernel.ErrorReason, message string) *kernel.AppError {
	err := kernel.NewAppError(http.StatusConflict, message)
	err.Reason = reason
	return err
}
