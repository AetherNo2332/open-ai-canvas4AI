package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"yingce/backend/internal/assets"
	canvasdomain "yingce/backend/internal/canvas"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type ServiceOptions struct {
	// Invoked within the same transaction immediately before document access.
	Authorize   func(context.Context, *repository.Repository, identity.Principal, string, string) error
	Validation  func(context.Context, *repository.Repository, identity.Principal) (ValidationContext, error)
	Quota       repository.CanvasWriteQuota
	AfterCommit func(Receipt)
}
type Service struct {
	repo    *repository.Repository
	options ServiceOptions
	mu      sync.Mutex
}

func NewService(repo *repository.Repository, options ServiceOptions) *Service {
	return &Service{repo: repo, options: options}
}
func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw[:])
}
func canonical(v any) (string, error) { raw, err := json.Marshal(v); return string(raw), err }
func (s *Service) authorize(ctx context.Context, tx *repository.Repository, p identity.Principal, canvasID, scope string) error {
	if p.ID == "" || p.OwnerID == "" || !p.HasScope(scope) || (scope == "canvas.write" && p.Role == "viewer") {
		return failure("permission_denied", "没有画布权限")
	}
	if err := tx.LockCanvasWrite(p.OwnerID, canvasID); err != nil {
		return err
	}
	user, err := tx.User(p.OwnerID)
	if err != nil || user.Status != model.UserStatusActive {
		return failure("permission_denied", "画布所有者不可用")
	}
	if s.options.Authorize != nil {
		return s.options.Authorize(ctx, tx, p, canvasID, scope)
	}
	if p.ID != p.OwnerID || p.Role != "owner" {
		return failure("permission_denied", "该画布未授权给当前操作者")
	}
	return nil
}
func validateClient(p identity.Principal, clientID, operationID string) error {
	if err := validateCanvasID(clientID, "客户端 ID", 128); err != nil {
		return err
	}
	if err := validateCanvasID(operationID, "操作 ID", 128); err != nil {
		return err
	}
	if p.ClientID != "" && p.ClientID != clientID {
		return failure("permission_denied", "客户端身份不匹配")
	}
	return nil
}
func (s *Service) state(tx *repository.Repository, p identity.Principal, id string, allowDeleted ...bool) (*model.CanvasProject, *model.CanvasDocumentEpoch, map[string]model.CanvasEntityVersion, Document, error) {
	row, err := tx.CanvasProjectForUser(p.OwnerID, id)
	if err != nil {
		return nil, nil, nil, nil, failure("entity_deleted", "画布不存在")
	}
	var doc Document
	if err = json.Unmarshal([]byte(row.PayloadJSON), &doc); err != nil || doc == nil {
		return nil, nil, nil, nil, failure("invalid_document", "画布文档不可读取")
	}
	epoch, err := tx.CommandEpoch(p.OwnerID, id)
	if tx.IsMissing(err) {
		epoch = &model.CanvasDocumentEpoch{CanvasID: id, OwnerID: p.OwnerID, Epoch: newID(), UpdatedAt: time.Now()}
		if err = tx.SaveCommandEpoch(epoch); err != nil {
			return nil, nil, nil, nil, err
		}
		versions := []model.CanvasEntityVersion{}
		for _, n := range maps(doc["nodes"]) {
			versions = append(versions, model.CanvasEntityVersion{CanvasID: id, Epoch: epoch.Epoch, EntityKey: "node:" + text(n["id"]), Version: row.Revision})
		}
		for _, e := range maps(doc["connections"]) {
			versions = append(versions, model.CanvasEntityVersion{CanvasID: id, Epoch: epoch.Epoch, EntityKey: "connection:" + text(e["id"]), Version: row.Revision})
		}
		if err = tx.SaveCommandVersions(versions); err != nil {
			return nil, nil, nil, nil, err
		}
	} else if err != nil {
		return nil, nil, nil, nil, err
	}
	if epoch.Deleted && (len(allowDeleted) == 0 || !allowDeleted[0]) {
		return nil, nil, nil, nil, failure("entity_deleted", "画布已删除")
	}
	versions, err := tx.CommandVersions(id, epoch.Epoch)
	return row, epoch, versions, doc, err
}
func snapshot(row *model.CanvasProject, epoch *model.CanvasDocumentEpoch, versions map[string]model.CanvasEntityVersion, doc Document) Snapshot {
	values := map[string]int64{}
	generations := map[string]int64{}
	for key, v := range versions {
		values[key] = v.Version
		generations[key] = v.Generation
	}
	out := Snapshot{Protocol: ProtocolVersion, CanvasID: row.ID, DocumentEpoch: epoch.Epoch, Revision: row.Revision, EventSeq: epoch.EventSeq, Document: doc, EntityVersions: values, EntityGenerations: generations}
	// 历史画布在读取投影上附带一次规范化预览：只读、幂等、不写文档、不分配标识。
	// 已规范化文档保持 nil，客户端因此能区分“不需要迁移”与“迁移后无冲突”。
	if !references.Enabled(doc) {
		preview := references.PreviewLegacy(doc, nil)
		out.ReferenceMigration = &preview
	}
	return out
}
func (s *Service) ReadSnapshot(ctx context.Context, p identity.Principal, id string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Snapshot
	err := s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, id, "canvas.read"); err != nil {
			return err
		}
		row, epoch, versions, doc, err := s.state(tx, p, id)
		if err != nil {
			return err
		}
		out = snapshot(row, epoch, versions, doc)
		return nil
	})
	return out, err
}
func (s *Service) Create(ctx context.Context, p identity.Principal, req CreateCanvasRequest) (Snapshot, error) {
	if err := validateClient(p, req.ClientID, req.OperationID); err != nil {
		return Snapshot{}, err
	}
	if utf8.RuneCountInString(req.Title) > 240 {
		return Snapshot{}, BadAuthRequest("画布标题超出限制")
	}
	body, err := canonical(req)
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Snapshot
	err = s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, "", "canvas.write"); err != nil {
			return err
		}
		key := model.CanvasCreateReceipt{OwnerID: p.OwnerID, ActorID: p.ID, ClientID: req.ClientID, OperationID: req.OperationID}
		previous, err := tx.CommandCreateReceipt(key)
		if err == nil {
			if previous.CanonicalJSON != body {
				return failure("operation_id_reused", "操作 ID 已用于不同的请求")
			}
			return json.Unmarshal([]byte(previous.SnapshotJSON), &out)
		}
		if !tx.IsMissing(err) {
			return err
		}
		id, epochID := newID(), newID()
		if req.ProjectID != "" {
			if _, err := tx.ProjectForUser(p.OwnerID, req.ProjectID); err != nil {
				return failure("permission_denied", "关联项目不存在或未授权")
			}
		}
		now := time.Now()
		title := req.Title
		if title == "" {
			title = "未命名画布"
		}
		doc := Document{"id": id, "referenceFieldsVersion": 1, "title": title, "nodes": []any{}, "connections": []any{}, "createdAt": now.UTC().Format(time.RFC3339Nano), "updatedAt": now.UTC().Format(time.RFC3339Nano)}
		if req.ProjectID != "" {
			doc["projectId"] = req.ProjectID
		}
		versions := map[string]model.CanvasEntityVersion{}
		if len(req.InitialOperations) > 0 {
			v, err := s.validation(ctx, tx, p, doc, req.InitialOperations)
			if err != nil {
				return err
			}
			plan, err := Plan(doc, Command{Protocol: ProtocolVersion, CanvasID: id, DocumentEpoch: epochID, ClientID: req.ClientID, OperationID: req.OperationID, Operations: req.InitialOperations}, v)
			if err != nil {
				return err
			}
			if plan.Document["_deleted"] == true {
				return BadAuthRequest("不能创建已删除画布")
			}
			doc = plan.Document
			for key := range plan.WriteSet {
				versions[key] = model.CanvasEntityVersion{CanvasID: id, Epoch: epochID, EntityKey: key, Version: 1}
			}
		}
		payload, err := canonical(doc)
		if err != nil {
			return err
		}
		row := &model.CanvasProject{ID: id, UserID: p.OwnerID, ProjectID: req.ProjectID, Title: title, PayloadJSON: payload, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if s.options.Quota != nil {
			if err = s.options.Quota(tx, p.OwnerID, true, int64(len(payload))); err != nil {
				return err
			}
		}
		if err = tx.CreateCommandCanvas(row); err != nil {
			return err
		}
		if err = canvasdomain.CaptureDocumentHistory(tx, nil, *row, "initial"); err != nil {
			return err
		}
		epoch := &model.CanvasDocumentEpoch{CanvasID: id, OwnerID: p.OwnerID, Epoch: epochID, UpdatedAt: now}
		if err = tx.SaveCommandEpoch(epoch); err != nil {
			return err
		}
		rows := []model.CanvasEntityVersion{}
		for _, v := range versions {
			rows = append(rows, v)
		}
		if err = tx.SaveCommandVersions(rows); err != nil {
			return err
		}
		for _, version := range rows {
			versions[version.EntityKey] = version
		}
		if err = tx.SaveCommandResources(p.OwnerID, id, epochID, req.OperationID, resourceIDs(doc), resourceIDs(doc)); err != nil {
			return err
		}
		out = snapshot(row, epoch, versions, doc)
		encoded, err := canonical(out)
		if err != nil {
			return err
		}
		key.CanonicalJSON, key.SnapshotJSON, key.CanvasID, key.CreatedAt = body, encoded, id, now
		return tx.InsertCommandCreateReceipt(&key)
	})
	return out, err
}
func receiptKey(p identity.Principal, req Command) model.CanvasCommandReceipt {
	return model.CanvasCommandReceipt{OwnerID: p.OwnerID, CanvasID: req.CanvasID, Epoch: req.DocumentEpoch, ActorID: p.ID, ClientID: req.ClientID, OperationID: req.OperationID}
}
func (s *Service) validation(ctx context.Context, tx *repository.Repository, p identity.Principal, doc Document, ops []Operation, canvasIDs ...string) (ValidationContext, error) {
	v := ValidationContext{Principal: p, Now: time.Now()}
	var err error
	if s.options.Validation != nil {
		v, err = s.options.Validation(ctx, tx, p)
		if err != nil {
			return v, err
		}
		v.Principal = p
		if v.Now.IsZero() {
			v.Now = time.Now()
		}
	}
	additionalResourcePolicy := v.ResourceAllowed
	owned := map[string]bool{}
	knownAssets := map[string]AssetMetadata{}
	v.Resources = map[string]ResourceMetadata{}
	v.CharacterBindings = map[string]map[string]any{}
	v.FolderBindings = map[string]bool{}
	loadAsset := func(id string) (AssetMetadata, error) {
		a, err := tx.AssetForUser(p.OwnerID, id)
		if err != nil {
			return AssetMetadata{}, failure("resource_not_owned", "素材不存在或未授权")
		}
		var payload map[string]any
		if err = json.Unmarshal([]byte(a.PayloadJSON), &payload); err != nil {
			return AssetMetadata{}, err
		}
		data, _ := payload["data"].(map[string]any)
		if a.Kind == "text" {
			_, hasContent := data["content"].(string)
			_, hasText := data["text"].(string)
			if !hasContent && !hasText {
				return AssetMetadata{}, BadAuthRequest("文本素材内容无效")
			}
		}
		resourceID := ""
		for _, key := range []string{"storageKey", "dataUrl", "url"} {
			if resourceID = assets.ResourceID(text(data[key])); resourceID != "" {
				break
			}
		}
		content := text(data["content"])
		if content == "" {
			content = text(data["text"])
		}
		result := AssetMetadata{ID: a.ID, Title: a.Title, Kind: a.Kind, Status: string(a.Status), Content: content, ResourceID: resourceID}
		if resourceID != "" {
			r, err := tx.ResourceForUser(p.OwnerID, resourceID)
			if err != nil || r.Status != model.ResourceStatusReady {
				return AssetMetadata{}, failure("resource_not_owned", "素材资源未就绪")
			}
			if r.Kind != a.Kind && !strings.HasPrefix(r.MimeType, a.Kind+"/") {
				return AssetMetadata{}, BadAuthRequest("素材资源类型与素材记录不符")
			}
			owned[resourceID] = additionalResourcePolicy == nil || additionalResourcePolicy(resourceID)
			result.MimeType, result.Size, result.Width, result.Height, result.DurationMs = r.MimeType, r.Size, r.Width, r.Height, r.DurationMs
		}
		return result, nil
	}
	for _, op := range ops {
		if op.Type == "update_node" && editsFolderBinding(op) {
			projectID, folderID := editedFolderBinding(doc, op)
			if projectID != "" || folderID != "" {
				if _, err := tx.ProjectForUser(p.OwnerID, projectID); err != nil {
					return v, failure("resource_not_owned", "文件夹来源项目不存在或未授权")
				}
				if _, err := tx.ProjectAssetFolder(projectID, folderID); err != nil {
					return v, failure("resource_not_owned", "项目素材文件夹不存在或未授权")
				}
				v.FolderBindings[projectID+"\x00"+folderID] = true
			}
		}
		if op.Type == "bind_character" {
			metadata, err := loadCharacterBinding(tx, p.OwnerID, op)
			if err != nil {
				return v, err
			}
			v.CharacterBindings[characterBindingKey(op)] = metadata
			if id := assets.ResourceID(text(metadata["characterCoverUrl"])); id != "" {
				owned[id] = additionalResourcePolicy == nil || additionalResourcePolicy(id)
			}
		}
		if op.Type == "bind_resource" {
			resource, err := tx.ResourceForUser(p.OwnerID, op.ResourceID)
			if err != nil || resource.Status != model.ResourceStatusReady {
				return v, failure("resource_not_owned", "资源不存在、未就绪或未授权")
			}
			owned[resource.ID] = additionalResourcePolicy == nil || additionalResourcePolicy(resource.ID)
			v.Resources[resource.ID] = ResourceMetadata{ID: resource.ID, Kind: resource.Kind, MimeType: resource.MimeType, Size: resource.Size, Width: resource.Width, Height: resource.Height, DurationMs: resource.DurationMs}
		}
		if op.Type == "use_media_version" {
			canvasID := text(doc["id"])
			if len(canvasIDs) > 0 {
				canvasID = canvasIDs[0]
			}
			scope, err := mediaversions.ScopeForNode(tx, p.OwnerID, canvasID, op.ID)
			if err != nil {
				return v, PersistenceError(err)
			}
			selection, err := mediaversions.PrepareSelection(tx, scope, op.VersionID)
			if err != nil {
				return v, PersistenceError(err)
			}
			if v.MediaVersionSelections == nil {
				v.MediaVersionSelections = map[string]mediaversions.Selection{}
			}
			v.MediaVersionSelections[op.VersionID] = selection
			owned[selection.Version.ResourceID] = additionalResourcePolicy == nil || additionalResourcePolicy(selection.Version.ResourceID)
		}
		if len(op.PrevisEntityOperations) > 0 {
			ids := map[string]bool{}
			for _, scene := range maps(doc["previsScenes"]) {
				if text(scene["id"]) == op.ID {
					for _, object := range maps(scene["objects"]) {
						if id := text(object["assetId"]); id != "" {
							ids[id] = true
						}
					}
				}
			}
			for _, change := range op.PrevisEntityOperations {
				if id := text(change.Patch["assetId"]); id != "" {
					ids[id] = true
				}
			}
			for id := range ids {
				if _, ok := knownAssets[id]; !ok {
					value, err := loadAsset(id)
					if err != nil {
						return v, err
					}
					knownAssets[id] = value
				}
			}
		}
		if op.Type == "edit_timeline" {
			mediaAssets := map[string]bool{}
			timeline, _ := doc["timeline"].(map[string]any)
			for _, clip := range maps(timeline["clips"]) {
				media, _ := clip["directMedia"].(map[string]any)
				if id := text(media["assetId"]); id != "" {
					mediaAssets[id] = true
				}
			}
			for _, change := range op.TimelineOperations {
				media, _ := change.Patch["directMedia"].(map[string]any)
				if id := text(media["assetId"]); id != "" {
					mediaAssets[id] = true
				}
			}
			for id := range mediaAssets {
				if _, ok := knownAssets[id]; !ok {
					value, err := loadAsset(id)
					if err != nil {
						return v, err
					}
					knownAssets[id] = value
				}
			}
		}
		if op.Type == "bind_asset" {
			if _, ok := knownAssets[op.AssetID]; ok {
				continue
			}
			value, err := loadAsset(op.AssetID)
			if err != nil {
				return v, err
			}
			knownAssets[op.AssetID] = value
		}
	}
	inputsOps := append([]Operation(nil), ops...)
	for i := range inputsOps {
		inputsOps[i].FieldBaselines = nil
	}
	encoded, err := json.Marshal(map[string]any{"operations": inputsOps})
	if err != nil {
		return v, err
	}
	var inputs Document
	if err = json.Unmarshal(encoded, &inputs); err != nil {
		return v, err
	}
	for _, source := range []Document{doc, inputs} {
		for _, id := range resourceIDs(source) {
			if _, ok := owned[id]; ok {
				continue
			}
			row, err := tx.ResourceForUser(p.OwnerID, id)
			if err != nil && !tx.IsMissing(err) {
				return v, err
			}
			owned[id] = err == nil && row.Status == model.ResourceStatusReady && (additionalResourcePolicy == nil || additionalResourcePolicy(id))
		}
	}
	// The planner sees only immutable metadata hydrated in this transaction.
	v.ResourceAllowed = func(id string) bool { return owned[id] }
	v.Asset = func(id string) (AssetMetadata, error) {
		value, ok := knownAssets[id]
		if !ok {
			return AssetMetadata{}, failure("resource_not_owned", "素材未授权")
		}
		return value, nil
	}
	return v, nil
}
func checkVersions(req Command, row *model.CanvasProject, versions map[string]model.CanvasEntityVersion, reads map[string]bool, fieldMerges ...map[string]bool) error {
	complete := len(req.ExpectedEntityVersions) > 0
	for key := range reads {
		if _, ok := req.ExpectedEntityVersions[key]; !ok {
			complete = false
			break
		}
	}
	if !complete {
		if req.BaseRevision != row.Revision {
			return failure("revision_conflict", "画布已变化，请读取最新版本后重试")
		}
		return nil
	}
	for key := range reads {
		if versions[key].Version != req.ExpectedEntityVersions[key] {
			if len(fieldMerges) == 1 && fieldMerges[0][key] {
				continue
			}
			return canvasFieldError(key, "entity_version_conflict", "目标实体已被其他操作修改")
		}
	}
	return nil
}

// Durable consumer shape for occupancy checks; matches tasks.ConsumerJSON without
// making the command layer depend on the task layer.
type activeCanvasConsumer struct {
	RowID        string `json:"rowId"`
	OutputNodeID string `json:"outputNodeId"`
}

// Storyboard output ownership is a durable claim. Row and output metadata can be
// reverted by history, so the accepted task bindings, not the metadata, decide
// whether a slot may be rebound.
func (s *Service) validateStoryboardOccupancy(tx *repository.Repository, p identity.Principal, canvasID, epoch string, ops []Operation) error {
	rows := map[string]map[string]bool{}
	outputs := map[string]bool{}
	for _, op := range ops {
		if op.Type != "bind_storyboard_output" {
			continue
		}
		if rows[op.ID] == nil {
			rows[op.ID] = map[string]bool{}
		}
		rows[op.ID][op.RowID] = true
		if op.SourceNodeID != "" {
			outputs[op.SourceNodeID] = true
		}
	}
	if len(rows) == 0 {
		return nil
	}
	bindings, err := tx.ActiveCanvasTaskBindings(p.OwnerID, canvasID, epoch)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		var consumer activeCanvasConsumer
		if err = json.Unmarshal([]byte(binding.ConsumerJSON), &consumer); err != nil {
			return err
		}
		for boardID, boardRows := range rows {
			if binding.TargetNodeID == boardID && boardRows[consumer.RowID] {
				return failure("reference_busy", "该分镜行仍有未完成任务，请先处理已有任务")
			}
		}
		if outputs[binding.TargetNodeID] || (consumer.OutputNodeID != "" && outputs[consumer.OutputNodeID]) {
			return failure("reference_busy", "该产物仍有未完成任务，请先处理已有任务")
		}
	}
	return nil
}
func (s *Service) Execute(ctx context.Context, p identity.Principal, req Command) (Receipt, error) {
	if err := validateClient(p, req.ClientID, req.OperationID); err != nil {
		return Receipt{}, err
	}
	body, err := canonical(req)
	if err != nil {
		return Receipt{}, err
	}
	s.mu.Lock()
	var out Receipt
	fresh := false
	err = s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, req.CanvasID, "canvas.write"); err != nil {
			return err
		}
		row, epoch, versions, doc, err := s.state(tx, p, req.CanvasID, true)
		if err != nil {
			return err
		}
		if req.DocumentEpoch != epoch.Epoch {
			return failure("document_epoch_mismatch", "画布已恢复到新的文档世代")
		}
		key := receiptKey(p, req)
		previous, err := tx.CommandReceipt(key)
		if err == nil {
			if previous.CanonicalJSON != body {
				return failure("operation_id_reused", "操作 ID 已用于不同的请求")
			}
			return json.Unmarshal([]byte(previous.ReceiptJSON), &out)
		}
		if !tx.IsMissing(err) {
			return err
		}
		if epoch.Deleted {
			return failure("entity_deleted", "画布已删除")
		}
		// 历史画布先完成一次规范化；放行规则见 referenceMigrationGate。
		if err := referenceMigrationGate(doc, req.Operations); err != nil {
			return err
		}
		present := map[string]bool{}
		for _, node := range maps(doc["nodes"]) {
			present[text(node["id"])] = true
		}
		for _, op := range req.Operations {
			switch op.Type {
			case "add_node":
				present[op.ID] = true
			case "update_node", "delete_node", "replace_text", "set_parent", "reorder_rows", "edit_drawing", "edit_storyboard", "edit_batch_table", "edit_image_batch", "bind_asset", "bind_resource", "bind_character", "bind_storyboard_output":
				if !present[op.ID] {
					return failure("entity_deleted", "目标节点已删除")
				}
				if op.Type == "delete_node" {
					delete(present, op.ID)
				}
			}
		}
		v, err := s.validation(ctx, tx, p, doc, req.Operations, row.ID)
		if err != nil {
			return err
		}
		if err = s.validateStoryboardOccupancy(tx, p, row.ID, epoch.Epoch, req.Operations); err != nil {
			return err
		}
		plan, err := Plan(doc, req, v)
		if err != nil {
			return err
		}
		for _, op := range req.Operations {
			if baseline := op.FieldBaselines; baseline != nil && baseline.Generation != nil {
				version, exists := versions["node:"+op.ID]
				if !exists || version.Deleted || version.Generation != *baseline.Generation {
					return canvasFieldError("node:"+op.ID, "entity_version_conflict", "节点已进入新的生命周期")
				}
			}
		}
		if err = checkVersions(req, row, versions, plan.ReadSet, plan.FieldMergeTargets); err != nil {
			return err
		}
		// An unchanged editor command still needs a durable ACK for recovery, but
		// must not create a document event or alter undo/redo. Host mutations and
		// same-value lifecycle writes keep their existing commit paths.
		if len(plan.WriteSet) == 0 && len(plan.Changes.Entities) == 0 && len(plan.Changes.Fields) == 0 &&
			plan.Changes.NodeOrder == nil && plan.Changes.ConnectionOrder == nil && len(plan.Changes.RecreatedEntities) == 0 {
			// Older clients interpret ACKs as events. Reuse only a cursor whose
			// document revision the caller has already observed.
			if req.BaseRevision != row.Revision {
				return failure("revision_conflict", "画布已变化，请读取最新版本后重试")
			}
			out = Receipt{
				Protocol: ProtocolVersion, CanvasID: row.ID, DocumentEpoch: epoch.Epoch,
				OperationID: req.OperationID, ClientID: req.ClientID, ActorID: p.ID,
				Revision: row.Revision, EventSeq: epoch.EventSeq, Changes: plan.Changes,
				EntityVersions: map[string]int64{}, EntityGenerations: map[string]int64{},
			}
			encoded, err := canonical(out)
			if err != nil {
				return err
			}
			key.CanonicalJSON, key.ReceiptJSON, key.CreatedAt = body, encoded, time.Now()
			if err = tx.InsertCommandReceipt(&key); err != nil {
				return err
			}
			out = Receipt{}
			return json.Unmarshal([]byte(encoded), &out)
		}
		if err = tx.AbandonRedo(p.OwnerID, row.ID, epoch.Epoch, p.ID, req.ClientID); err != nil {
			return err
		}
		out, err = s.persist(tx, p, req, body, row, epoch, doc, plan, true)
		fresh = err == nil
		return err
	})
	s.mu.Unlock()
	if err == nil && fresh && s.options.AfterCommit != nil {
		s.options.AfterCommit(out)
	}
	return out, err
}
func (s *Service) persist(tx *repository.Repository, p identity.Principal, req Command, body string, row *model.CanvasProject, epoch *model.CanvasDocumentEpoch, before Document, plan PlanResult, history bool, snapshotReasons ...string) (Receipt, error) {
	prior := *row
	oldRevision := row.Revision
	row.Revision++
	epoch.EventSeq++
	epoch.UpdatedAt = time.Now()
	row.UpdatedAt = epoch.UpdatedAt
	payload, err := canonical(plan.Document)
	if err != nil {
		return Receipt{}, err
	}
	if s.options.Quota != nil {
		if err = s.options.Quota(tx, p.OwnerID, false, int64(len(payload)-len(row.PayloadJSON))); err != nil {
			return Receipt{}, err
		}
	}
	row.PayloadJSON = payload
	epoch.Deleted = plan.Document["_deleted"] == true
	if title, ok := plan.Document["title"].(string); ok {
		row.Title = title
	}
	if err = tx.PersistCommandCanvas(row, oldRevision); err != nil {
		return Receipt{}, err
	}
	reason := "automatic"
	if len(snapshotReasons) > 0 {
		reason = snapshotReasons[0]
	}
	if err = canvasdomain.CaptureDocumentHistory(tx, &prior, *row, reason); err != nil {
		return Receipt{}, err
	}
	expected := map[string]int64{}
	versions := []model.CanvasEntityVersion{}
	for key := range plan.WriteSet {
		expected[key] = row.Revision
		deleted := false
		for _, change := range plan.Changes.Entities {
			if change.Key == key {
				deleted = change.Delete
			}
		}
		versions = append(versions, model.CanvasEntityVersion{CanvasID: row.ID, Epoch: epoch.Epoch, EntityKey: key, Version: row.Revision, Deleted: deleted})
	}
	if err = tx.SaveCommandVersions(versions, plan.Changes.RecreatedEntities); err != nil {
		return Receipt{}, err
	}
	if err = tx.FenceChangedMediaOutputOwners(p.OwnerID, row.ID, epoch.Epoch, maps(before["nodes"]), maps(plan.Document["nodes"]), plan.Changes.RecreatedEntities); err != nil {
		return Receipt{}, err
	}
	out := Receipt{Protocol: ProtocolVersion, CanvasID: row.ID, DocumentEpoch: epoch.Epoch, OperationID: req.OperationID, ClientID: req.ClientID, ActorID: p.ID, Revision: row.Revision, EventSeq: epoch.EventSeq, Changes: plan.Changes}
	out.EntityVersions = expected
	out.EntityGenerations = map[string]int64{}
	for _, version := range versions {
		out.EntityGenerations[version.EntityKey] = version.Generation
	}
	encoded, err := canonical(out)
	if err != nil {
		return Receipt{}, err
	}
	key := receiptKey(p, req)
	key.CanonicalJSON, key.ReceiptJSON, key.CreatedAt = body, encoded, time.Now()
	if err = tx.InsertCommandReceipt(&key); err != nil {
		return Receipt{}, err
	}
	if err = tx.InsertCommandEvent(&model.CanvasCommandEvent{CanvasID: row.ID, Sequence: epoch.EventSeq, OwnerID: p.OwnerID, Epoch: epoch.Epoch, OperationID: req.OperationID, EventJSON: encoded, CreatedAt: time.Now()}); err != nil {
		return Receipt{}, err
	}
	if err = tx.SaveCommandEpoch(epoch); err != nil {
		return Receipt{}, err
	}
	historyResources := resourceIDs(before)
	historyResources = append(historyResources, plan.ResourceIDs...)
	if err = tx.SaveCommandResources(p.OwnerID, row.ID, epoch.Epoch, req.OperationID, plan.ResourceIDs, historyResources); err != nil {
		return Receipt{}, err
	}
	if history {
		forward, err := canonical(plan.Changes)
		if err != nil {
			return Receipt{}, err
		}
		inverse, err := canonical(plan.Inverse)
		if err != nil {
			return Receipt{}, err
		}
		expect, err := canonical(expected)
		if err != nil {
			return Receipt{}, err
		}
		err = tx.SaveActorHistory(&model.CanvasActorHistory{ID: newID(), CanvasID: row.ID, Epoch: epoch.Epoch, OwnerID: p.OwnerID, ActorID: p.ID, ClientID: req.ClientID, OperationID: req.OperationID, Sequence: epoch.EventSeq, State: "applied", ForwardJSON: forward, InverseJSON: inverse, ExpectedVersionsJSON: expect, CreatedAt: time.Now()})
		if err != nil {
			return Receipt{}, err
		}
	}
	// Return the exact persisted wire representation, including numeric JSON
	// values and omitted empty fields, so retries are identical to the first ACK.
	if err = tx.PruneCanvasMediaVersions(p.OwnerID, row.ID); err != nil {
		return Receipt{}, err
	}
	var durable Receipt
	if err = json.Unmarshal([]byte(encoded), &durable); err != nil {
		return Receipt{}, err
	}
	return durable, nil
}
func (s *Service) GetReceipt(ctx context.Context, p identity.Principal, canvasID, epoch, clientID, operationID string) (Receipt, error) {
	if err := validateClient(p, clientID, operationID); err != nil {
		return Receipt{}, err
	}
	var out Receipt
	err := s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, canvasID, "canvas.read"); err != nil {
			return err
		}
		row, err := tx.CommandReceipt(receiptKey(p, Command{CanvasID: canvasID, DocumentEpoch: epoch, ClientID: clientID, OperationID: operationID}))
		if tx.IsMissing(err) {
			return failure("receipt_missing", "命令尚未提交")
		}
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(row.ReceiptJSON), &out)
	})
	return out, err
}
func (s *Service) ReadEvents(ctx context.Context, p identity.Principal, canvasID string, after int64, limit int) ([]Receipt, error) {
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, BadAuthRequest("事件游标或数量无效")
	}
	out := []Receipt{}
	err := s.repo.CommandTransaction(ctx, func(tx *repository.Repository) error {
		if err := s.authorize(ctx, tx, p, canvasID, "canvas.read"); err != nil {
			return err
		}
		rows, err := tx.CommandEvents(p.OwnerID, canvasID, after, limit)
		if err != nil {
			return err
		}
		for _, row := range rows {
			var event Receipt
			if err = json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
				return err
			}
			out = append(out, event)
		}
		return nil
	})
	return out, err
}
