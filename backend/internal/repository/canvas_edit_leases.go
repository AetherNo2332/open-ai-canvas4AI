package repository

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

var ErrCanvasEditLease = errors.New("节点正在其他窗口编辑，或编辑权限已过期；本地草稿已保留")

func (r *Repository) WithCanvasEditProofs(proofs []model.CanvasEditProof) *Repository {
	copy := *r
	copy.canvasEditProofs = proofs
	return &copy
}

func (r *Repository) CanvasEditLeases(userID, canvasID string, now time.Time) ([]model.CanvasEditLease, error) {
	leases := []model.CanvasEditLease{}
	err := r.db.Where("user_id = ? AND canvas_id = ? AND expires_at > ?", userID, canvasID, now).Find(&leases).Error
	return leases, err
}

// Call inside a transaction after locking the parent canvas, including renew/release.
func (r *Repository) ChangeCanvasEditLease(userID, canvasID string, proof model.CanvasEditProof, action, newToken string, now time.Time) (lease *model.CanvasEditLease, resultErr error) {
	defer func() {
		if resultErr == nil {
			r.canvasChanged(userID, canvasID)
		}
	}()
	var current model.CanvasEditLease
	err := r.db.First(&current, "user_id = ? AND canvas_id = ? AND node_id = ?", userID, canvasID, proof.NodeID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	active := err == nil && current.ExpiresAt.After(now)
	matches := active && current.SessionID == proof.SessionID && current.Token == proof.Token
	if action == "release" {
		if !matches {
			return nil, ErrCanvasEditLease
		}
		return nil, r.db.Delete(&current).Error
	}
	if action == "renew" && !matches {
		return nil, ErrCanvasEditLease
	}
	if action == "acquire" && active && !matches {
		return nil, ErrCanvasEditLease
	}
	if !active {
		current = model.CanvasEditLease{UserID: userID, CanvasID: canvasID, NodeID: proof.NodeID, SessionID: proof.SessionID, Token: newToken}
	}
	current.ExpiresAt = now.Add(30 * time.Second)
	return &current, r.db.Save(&current).Error
}

// LeaseHolder 说明一次画布写入以谁的身份接受编辑租约判定。浏览器写入的编辑凭证由
// WithCanvasEditProofs 随仓储携带（一次请求可覆盖多个节点）；Agent 写入没有凭证。
type LeaseHolder struct {
	Kind string // LeaseHolderBrowser | LeaseHolderAgent
}

const (
	LeaseHolderBrowser = "browser"
	LeaseHolderAgent   = "agent"
)

var (
	browserLeaseHolder = LeaseHolder{Kind: LeaseHolderBrowser}
	agentLeaseHolder   = LeaseHolder{Kind: LeaseHolderAgent}
)

// checkCanvasEditLeasesFor 是浏览器与 Agent 共用的编辑租约判定，必须与 revision CAS 在同一
// 事务里调用。只比较作者字段（canvasLeaseAuthoredChanged），结果/进度写入不需要租约。
// after 为 nil 表示删除整张画布。
//   - 浏览器：凭证只能为自己持有的有效租约作证；有效租约下的作者字段改动必须带匹配凭证，
//     否则返回 ErrCanvasEditLease。
//   - Agent：没有凭证，任何有效租约下的作者字段改动或节点删除都返回
//     CanvasEditLeaseError{Reason: "node_locked"}，保留用户正在编辑的草稿。
func (r *Repository) checkCanvasEditLeasesFor(before, after *model.CanvasProject, holder LeaseHolder) error {
	if before == nil {
		return nil
	}
	if holder.Kind != LeaseHolderBrowser && holder.Kind != LeaseHolderAgent {
		return errors.New("unknown canvas edit lease holder")
	}
	leases, err := r.CanvasEditLeases(before.UserID, before.ID, time.Now().UTC())
	if err != nil {
		return err
	}
	proofs := r.canvasEditProofs
	if holder.Kind == LeaseHolderAgent {
		proofs = nil
	}
	if len(leases) == 0 && len(proofs) == 0 {
		return nil
	}
	oldNodes, err := canvasLeaseNodes(before.PayloadJSON)
	if err != nil {
		return err
	}
	newNodes := map[string]map[string]any{}
	if after != nil {
		newNodes, err = canvasLeaseNodes(after.PayloadJSON)
		if err != nil {
			return err
		}
	}
	byNode := map[string]model.CanvasEditLease{}
	for _, lease := range leases {
		byNode[lease.NodeID] = lease
	}
	for _, proof := range proofs {
		if !canvasLeaseAuthoredChanged(oldNodes[proof.NodeID], newNodes[proof.NodeID]) {
			continue
		}
		lease, exists := byNode[proof.NodeID]
		if !exists || lease.Token != proof.Token || lease.SessionID != proof.SessionID {
			return ErrCanvasEditLease
		}
	}
	for _, lease := range leases {
		if after != nil && !canvasLeaseAuthoredChanged(oldNodes[lease.NodeID], newNodes[lease.NodeID]) {
			continue
		}
		matched := false
		for _, proof := range proofs {
			if proof.NodeID == lease.NodeID && proof.SessionID == lease.SessionID && proof.Token == lease.Token {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if holder.Kind == LeaseHolderAgent {
			return &CanvasEditLeaseError{Reason: "node_locked", CanvasID: before.ID, NodeID: lease.NodeID}
		}
		return ErrCanvasEditLease
	}
	return nil
}

func canvasLeaseNodes(raw string) (map[string]map[string]any, error) {
	var document struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil, err
	}
	nodes := map[string]map[string]any{}
	for _, node := range document.Nodes {
		if id, ok := node["id"].(string); ok {
			nodes[id] = node
		}
	}
	return nodes, nil
}

// canvasLeaseAuthoredChanged 报告节点的作者字段是否变化（含节点新增或删除）：标题、
// 文本正文、提示词、生成合同（提示词、模型、选项与引用绑定）、inputBindings 以及
// 分镜 referenceNodeIds。结果、进度和任务状态等字段不在其中，持有租约时仍可回写。
func canvasLeaseAuthoredChanged(before, after map[string]any) bool {
	if before == nil || after == nil {
		return !reflect.DeepEqual(before, after)
	}
	if !reflect.DeepEqual(before["title"], after["title"]) {
		return true
	}
	b, _ := before["metadata"].(map[string]any)
	a, _ := after["metadata"].(map[string]any)
	for _, field := range []string{"content", "richText", "prompt", "composerContent", "generationSpec", "inputBindings"} {
		if (field == "content" || field == "richText") && before["type"] != "text" && before["type"] != "markdown" && before["type"] != "script" {
			continue
		}
		if !reflect.DeepEqual(b[field], a[field]) {
			return true
		}
	}
	bs, _ := b["storyboard"].(map[string]any)
	as, _ := a["storyboard"].(map[string]any)
	return !reflect.DeepEqual(bs["referenceNodeIds"], as["referenceNodeIds"])
}
