package canvas

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/repository"
)

type CanvasEditProof = repository.CanvasEditProof
type CanvasEditLeaseGrant = repository.CanvasEditLeaseGrant
type CanvasActiveEditLease struct {
	CanvasID      string    `json:"canvasId"`
	NodeID        string    `json:"nodeId"`
	SessionID     string    `json:"sessionId"`
	ExpiresAt     time.Time `json:"expiresAt"`
	NodeTitle     string    `json:"nodeTitle"`
	NodeType      string    `json:"nodeType"`
	NodeTypeLabel string    `json:"nodeTypeLabel"`
	NodeExists    bool      `json:"nodeExists"`
}

const CanvasEditProofHeader = repository.CanvasEditProofHeader

func ParseCanvasEditProofHeader(ctx context.Context, header string) (context.Context, error) {
	if header == "" {
		return ctx, nil
	}
	if len(header) > 32<<10 {
		return ctx, kernel.BadAuthRequest("编辑证明过大")
	}
	var proofs []CanvasEditProof
	decoder := json.NewDecoder(strings.NewReader(header))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proofs); err != nil || proofs == nil {
		return ctx, kernel.BadAuthRequest("编辑证明格式无效")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ctx, kernel.BadAuthRequest("编辑证明格式无效")
	}
	if err := repository.ValidateCanvasEditProofs(proofs); err != nil {
		return ctx, kernel.BadAuthRequest("编辑证明格式无效")
	}
	return repository.WithCanvasEditProofs(ctx, proofs), nil
}
func CanvasEditLeaseAppError(err error) *kernel.AppError {
	var lease *repository.CanvasEditLeaseError
	if !errors.As(err, &lease) {
		return nil
	}
	message := "节点正在另一编辑会话中编辑，请保留草稿后重试"
	if lease.Reason == "edit_proof_invalid" {
		message = "编辑证明已失效，请保留草稿并重新获取租约"
	}
	return &kernel.AppError{Status: 409, Code: 409, Reason: kernel.ErrorReason(lease.Reason), Message: message, Cause: err, Details: map[string]any{"canvasId": lease.CanvasID, "nodeId": lease.NodeID}}
}
func (s *Service) AcquireCanvasEditLease(ctx context.Context, owner, canvasID string, proof CanvasEditProof) (CanvasEditLeaseGrant, error) {
	proof.CanvasID = canvasID
	if err := repository.ValidateCanvasEditProofs([]CanvasEditProof{proof}); err != nil {
		return CanvasEditLeaseGrant{}, kernel.BadAuthRequest("节点、会话或编辑证明格式无效")
	}
	out, err := s.repo.WithContext(ctx).AcquireCanvasEditLease(owner, canvasID, proof)
	return out, s.canvasSyncError(err)
}
func (s *Service) RenewCanvasEditLease(ctx context.Context, owner, canvasID string, proof CanvasEditProof) (CanvasEditLeaseGrant, error) {
	proof.CanvasID = canvasID
	if err := repository.ValidateCanvasEditProofs([]CanvasEditProof{proof}); err != nil {
		return CanvasEditLeaseGrant{}, kernel.BadAuthRequest("节点、会话或编辑证明格式无效")
	}
	out, err := s.repo.WithContext(ctx).RenewCanvasEditLease(owner, canvasID, proof)
	return out, s.canvasSyncError(err)
}
func (s *Service) ReleaseCanvasEditLease(ctx context.Context, owner, canvasID string, proof CanvasEditProof) error {
	proof.CanvasID = canvasID
	if err := repository.ValidateCanvasEditProofs([]CanvasEditProof{proof}); err != nil {
		return kernel.BadAuthRequest("节点、会话或编辑证明格式无效")
	}
	return s.canvasSyncError(s.repo.WithContext(ctx).ReleaseCanvasEditLease(owner, canvasID, proof))
}
func (s *Service) canvasSyncError(err error) error {
	if err == nil {
		return nil
	}
	if public := CanvasEditLeaseAppError(err); public != nil {
		return public
	}
	if s.repo.IsMissing(err) {
		return kernel.NotFound("画布或节点不存在")
	}
	return err
}

type CanvasActiveAgent struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	UpdatedAt      time.Time `json:"updatedAt"`
}
type CanvasSyncStatus struct {
	Revision      int64                   `json:"revision"`
	DocumentEpoch string                  `json:"documentEpoch"`
	EventSeq      int64                   `json:"eventSeq"`
	Leases        []CanvasActiveEditLease `json:"leases"`
	ActiveAgents  []CanvasActiveAgent     `json:"activeAgents"`
}

func (s *Service) CanvasSyncStatus(ctx context.Context, owner, canvasID string) (CanvasSyncStatus, error) {
	out := CanvasSyncStatus{Leases: []CanvasActiveEditLease{}, ActiveAgents: []CanvasActiveAgent{}}
	err := s.repo.CanvasReadTransaction(ctx, func(tx *repository.Repository) error {
		row, err := tx.CanvasProjectMetadata(owner, canvasID)
		if err != nil {
			return err
		}
		out.Revision = row.Revision
		epoch, err := tx.LiveCanvasEpoch(owner, canvasID)
		if err != nil {
			return err
		}
		if epoch != nil {
			out.DocumentEpoch, out.EventSeq = epoch.Epoch, epoch.EventSeq
		}
		leases, err := tx.ActiveCanvasEditLeases(owner, canvasID)
		if err != nil {
			return err
		}
		if len(leases) > 0 {
			full, err := tx.CanvasProjectForUser(owner, canvasID)
			if err != nil {
				return err
			}
			names, err := canvasLeaseNodeNames(full.PayloadJSON)
			if err != nil {
				return err
			}
			for _, lease := range leases {
				item := CanvasActiveEditLease{
					CanvasID: lease.CanvasID, NodeID: lease.NodeID, SessionID: lease.SessionID, ExpiresAt: lease.ExpiresAt,
					NodeTitle: "节点已删除（" + canvasLeaseShortID(lease.NodeID) + "）", NodeTypeLabel: "节点",
				}
				if node, exists := names[lease.NodeID]; exists {
					item.NodeTitle, item.NodeType, item.NodeTypeLabel, item.NodeExists = node.title, node.kind, node.label, true
				}
				out.Leases = append(out.Leases, item)
			}
		}
		agents, err := tx.CanvasActiveAgentSummaries(owner, canvasID)
		if err != nil {
			return err
		}
		for _, agent := range agents {
			out.ActiveAgents = append(out.ActiveAgents, CanvasActiveAgent{ID: agent.ID, ConversationID: agent.ConversationID, Title: agent.Title, Status: agent.Status, UpdatedAt: agent.UpdatedAt})
		}
		return nil
	})
	return out, s.canvasSyncError(err)
}

type canvasLeaseNodeName struct{ title, kind, label string }

func canvasLeaseNodeNames(payload string) (map[string]canvasLeaseNodeName, error) {
	var doc struct {
		Nodes []struct {
			ID, Type, Title string
			Metadata        struct{ WorkflowKind string }
		}
	}
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return nil, errors.New("画布节点信息暂不可用")
	}
	counts := map[string]int{}
	for _, node := range doc.Nodes {
		if title := strings.TrimSpace(node.Title); title != "" {
			counts[title]++
		}
	}
	out := map[string]canvasLeaseNodeName{}
	registry := capability.BuiltinRegistry()
	for _, node := range doc.Nodes {
		kind, label := node.Type, "节点"
		if descriptor, ok := registry.ResolveNode(node.Type, node.Metadata.WorkflowKind); ok {
			kind, label = descriptor.Type, descriptor.Label
		}
		title := strings.TrimSpace(node.Title)
		if title == "" {
			title = "未命名" + label + "（" + canvasLeaseShortID(node.ID) + "）"
		} else if counts[title] > 1 {
			title += "（" + canvasLeaseShortID(node.ID) + "）"
		}
		out[node.ID] = canvasLeaseNodeName{title, kind, label}
	}
	return out, nil
}

func canvasLeaseShortID(id string) string {
	runes := []rune(id)
	if len(runes) <= 12 {
		return id
	}
	return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
}
