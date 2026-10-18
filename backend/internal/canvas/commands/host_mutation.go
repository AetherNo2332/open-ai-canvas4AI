package commands

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/references"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
)

// HostMutation is an internal composition seam, never a JSON/HTTP operation.
// The callback may compose task rows in the SAME transaction. It must not call
// upstream providers, start nested write transactions, or publish before commit.
type HostMutation func(*repository.Repository, Document) (Document, map[string]bool, error)

func (s *Service) ExecuteHost(ctx context.Context, p identity.Principal, req Command, domain string, input any, history bool, mutate HostMutation, source ...identity.Principal) (Receipt, bool, error) {
	attributed := p
	if len(source) > 0 {
		if len(source) != 1 || domain != "task_result" || p.ID != p.OwnerID || p.Role != "owner" || source[0].OwnerID != p.OwnerID || source[0].ClientID != req.ClientID || source[0].ID == "" {
			return Receipt{}, false, failure("permission_denied", "任务结果归属无效")
		}
		attributed = source[0]
	}
	if req.Protocol != ProtocolVersion {
		return Receipt{}, false, failure("protocol_mismatch", "命令协议不兼容")
	}
	if domain != "task_submit" && domain != "task_result" {
		return Receipt{}, false, BadAuthRequest("内部命令类型无效")
	}
	if err := validateClient(p, req.ClientID, req.OperationID); err != nil {
		return Receipt{}, false, err
	}
	for _, id := range []string{req.CanvasID, req.DocumentEpoch} {
		if err := validateCanvasID(id, "画布标识", 128); err != nil {
			return Receipt{}, false, err
		}
	}
	body, err := canonical(map[string]any{"domain": domain, "command": req, "input": input})
	if err != nil {
		return Receipt{}, false, err
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
			return failure("document_epoch_mismatch", "文档世代已改变")
		}
		prior, err := tx.CommandReceipt(receiptKey(attributed, req))
		if err == nil {
			if prior.CanonicalJSON != body {
				return failure("operation_id_reused", "操作 ID 已用于不同请求")
			}
			return json.Unmarshal([]byte(prior.ReceiptJSON), &out)
		}
		if !tx.IsMissing(err) {
			return err
		}
		if epoch.Deleted {
			return failure("entity_deleted", "画布已删除")
		}
		clone, err := cloneDocument(doc)
		if err != nil {
			return err
		}
		// 生成（task_submit）必须建立在已规范化的字段之上。task_result 不做这个门禁：
		// 在途任务的迟到结果仍必须落库，否则会丢资源、产物和任务归属；迁移命令不向
		// Agent 暴露，所以历史画布先由画布客户端完成一次规范化。
		if domain == "task_submit" && !references.Enabled(clone) {
			return referenceMigrationRequired()
		}
		next, reads, err := mutate(tx, clone)
		if err != nil {
			return err
		}
		coordinated, err := reconcileReferenceFields(doc, next, nil)
		if err != nil {
			return err
		}
		next = Document(coordinated.Document)
		if reads == nil {
			reads = map[string]bool{}
		}
		for _, id := range coordinated.ReadNodeIDs {
			reads["node:"+id] = true
		}
		for _, id := range coordinated.ReadEdgeIDs {
			reads["connection:"+id] = true
		}
		if err = validateResult(next); err != nil {
			return err
		}
		// Submission must respect the caller's observed entities. Results instead
		// check their durable task binding inside mutate, against the current node.
		if domain == "task_submit" {
			if err = checkVersions(req, row, versions, reads); err != nil {
				return err
			}
		}
		changes := Diff(doc, next)
		writes := map[string]bool{}
		for _, c := range changes.Entities {
			writes[c.Key] = true
			if drawingPresent(entity(doc, c.Key)) || drawingPresent(c.Value) {
				writes["drawing:"+c.Key[len("node:"):]] = true
			}
		}
		for k := range changes.Fields {
			writes["project:"+k] = true
		}
		if changes.NodeOrder != nil {
			writes["project:nodeOrder"] = true
		}
		if changes.ConnectionOrder != nil {
			writes["project:connectionOrder"] = true
		}
		resources := resourceIDs(next)
		for _, id := range resources {
			r, e := tx.ResourceForUser(p.OwnerID, id)
			if e != nil || r.Status != "ready" {
				return failure("resource_not_owned", "结果资源未就绪或未授权")
			}
		}
		plan := PlanResult{Document: next, Changes: changes, Inverse: Diff(next, doc), ReadSet: reads, WriteSet: writes, ResourceIDs: resources}
		if history {
			if err := tx.AbandonRedo(p.OwnerID, row.ID, epoch.Epoch, attributed.ID, req.ClientID); err != nil {
				return err
			}
		}
		out, err = s.persist(tx, attributed, req, body, row, epoch, doc, plan, history)
		fresh = err == nil
		return err
	})
	s.mu.Unlock()
	if err == nil && fresh && s.options.AfterCommit != nil {
		s.options.AfterCommit(out)
	}
	return out, fresh, err
}
func cloneDocument(doc Document) (Document, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var next Document
	err = json.Unmarshal(raw, &next)
	return next, err
}
