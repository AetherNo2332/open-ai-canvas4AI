package repository

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

const CanvasEditLeaseTTL = 30 * time.Second
const CanvasEditProofHeader = "X-Canvas-Edit-Proofs"

var ErrCanvasNodeLocked = errors.New("canvas node author fields are leased")
var ErrCanvasEditProofInvalid = errors.New("canvas edit proof is invalid or expired")

type CanvasEditLeaseError struct {
	Reason   string
	CanvasID string
	NodeID   string
}

func (e *CanvasEditLeaseError) Error() string { return e.Reason + ": " + e.CanvasID + "/" + e.NodeID }
func (e *CanvasEditLeaseError) Unwrap() error {
	if e.Reason == "node_locked" {
		return ErrCanvasNodeLocked
	}
	return ErrCanvasEditProofInvalid
}

type CanvasEditProof struct {
	CanvasID  string `json:"canvasId"`
	NodeID    string `json:"nodeId"`
	SessionID string `json:"sessionId"`
	Token     string `json:"token"`
}
type CanvasEditLeaseGrant struct {
	CanvasEditProof
	ExpiresAt time.Time `json:"expiresAt"`
}
type canvasEditProofContextKey struct{}

// WithCanvasEditProofs is request scoped; GORM transaction/savepoint contexts
// and Repository copies preserve it without mutable service-level state.
func WithCanvasEditProofs(ctx context.Context, proofs []CanvasEditProof) context.Context {
	return context.WithValue(ctx, canvasEditProofContextKey{}, append([]CanvasEditProof(nil), proofs...))
}
func validLeaseID(s string) bool {
	return len(s) > 0 && len(s) <= 128 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}
func validLeaseToken(s string) bool {
	if len(s) < 32 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func ValidateCanvasEditProofs(proofs []CanvasEditProof) error {
	if len(proofs) > 128 {
		return fmt.Errorf("too many canvas edit proofs")
	}
	seen := map[string]bool{}
	for _, p := range proofs {
		key := p.CanvasID + "\x00" + p.NodeID
		if !validLeaseID(p.CanvasID) || !validLeaseID(p.NodeID) || !validLeaseID(p.SessionID) || !validLeaseToken(p.Token) || seen[key] {
			return fmt.Errorf("invalid canvas edit proof")
		}
		seen[key] = true
	}
	return nil
}
func leaseFailure(reason, canvas, node string) error {
	return &CanvasEditLeaseError{Reason: reason, CanvasID: canvas, NodeID: node}
}
func matchingLease(row model.CanvasNodeEditLease, p CanvasEditProof) bool {
	return row.CanvasID == p.CanvasID && row.NodeID == p.NodeID && row.SessionID == p.SessionID && subtle.ConstantTimeCompare([]byte(row.Token), []byte(p.Token)) == 1
}
func leaseGrant(row model.CanvasNodeEditLease) CanvasEditLeaseGrant {
	return CanvasEditLeaseGrant{CanvasEditProof: CanvasEditProof{CanvasID: row.CanvasID, NodeID: row.NodeID, SessionID: row.SessionID, Token: row.Token}, ExpiresAt: row.ExpiresAt}
}
func (r *Repository) mutateCanvasEditLease(ownerID, canvasID string, proof CanvasEditProof, action string) (CanvasEditLeaseGrant, error) {
	if proof.CanvasID != "" && proof.CanvasID != canvasID {
		return CanvasEditLeaseGrant{}, leaseFailure("edit_proof_invalid", canvasID, proof.NodeID)
	}
	proof.CanvasID = canvasID
	if err := ValidateCanvasEditProofs([]CanvasEditProof{proof}); err != nil {
		return CanvasEditLeaseGrant{}, err
	}
	var out CanvasEditLeaseGrant
	err := r.db.Transaction(func(db *gorm.DB) error {
		tx := r.transactionRepository(db)
		if err := tx.LockCanvasWrite(ownerID, canvasID); err != nil {
			return err
		}
		canvas, err := tx.CanvasProjectForUser(ownerID, canvasID)
		if err != nil {
			return err
		}
		if action != "release" {
			if _, err := tx.LiveCanvasEpoch(ownerID, canvasID); err != nil {
				return err
			}
			nodes, err := canvasLeaseNodes(canvas.PayloadJSON)
			if err != nil {
				return err
			}
			if nodes[proof.NodeID] == nil {
				return gorm.ErrRecordNotFound
			}
		}
		var row model.CanvasNodeEditLease
		err = db.Where("owner_id = ? AND canvas_id = ? AND node_id = ?", ownerID, canvasID, proof.NodeID).First(&row).Error
		exists := err == nil
		if err != nil && !tx.IsMissing(err) {
			return err
		}
		now := time.Now().UTC()
		active := exists && row.ExpiresAt.After(now)
		switch action {
		case "acquire":
			if active && !matchingLease(row, proof) {
				if row.SessionID == proof.SessionID {
					return leaseFailure("edit_proof_invalid", canvasID, proof.NodeID)
				}
				return leaseFailure("node_locked", canvasID, proof.NodeID)
			}
			row = model.CanvasNodeEditLease{OwnerID: ownerID, CanvasID: canvasID, NodeID: proof.NodeID, SessionID: proof.SessionID, Token: proof.Token, ExpiresAt: now.Add(CanvasEditLeaseTTL)}
			if err := db.Save(&row).Error; err != nil {
				return err
			}
			out = leaseGrant(row)
		case "renew", "release":
			if !active || !matchingLease(row, proof) {
				return leaseFailure("edit_proof_invalid", canvasID, proof.NodeID)
			}
			if action == "release" {
				return db.Delete(&row).Error
			}
			row.ExpiresAt = now.Add(CanvasEditLeaseTTL)
			if err := db.Save(&row).Error; err != nil {
				return err
			}
			out = leaseGrant(row)
		}
		return nil
	})
	return out, err
}
func (r *Repository) AcquireCanvasEditLease(ownerID, canvasID string, proof CanvasEditProof) (CanvasEditLeaseGrant, error) {
	return r.mutateCanvasEditLease(ownerID, canvasID, proof, "acquire")
}
func (r *Repository) RenewCanvasEditLease(ownerID, canvasID string, proof CanvasEditProof) (CanvasEditLeaseGrant, error) {
	return r.mutateCanvasEditLease(ownerID, canvasID, proof, "renew")
}
func (r *Repository) ReleaseCanvasEditLease(ownerID, canvasID string, proof CanvasEditProof) error {
	_, err := r.mutateCanvasEditLease(ownerID, canvasID, proof, "release")
	return err
}
func (r *Repository) ActiveCanvasEditLeases(ownerID, canvasID string) ([]model.CanvasNodeEditLease, error) {
	rows := []model.CanvasNodeEditLease{}
	now := time.Now().UTC()
	query := r.db.Select("canvas_id", "node_id", "session_id", "expires_at").Where("owner_id = ? AND canvas_id = ?", ownerID, canvasID)
	// SQLite compares offset-bearing time strings lexically. Scoped Go filtering
	// also handles the short-lived non-UTC rows written before normalization.
	if r.Dialect() != "sqlite" {
		query = query.Where("expires_at > ?", now)
	}
	if err := query.Order("node_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	active := make([]model.CanvasNodeEditLease, 0, len(rows))
	for _, row := range rows {
		if row.ExpiresAt.After(now) {
			row.ExpiresAt = row.ExpiresAt.UTC()
			active = append(active, row)
		}
	}
	return active, nil
}

// Only explicit summary columns are read; StateJSON/transcripts are not needed
// for synchronization and multiple concurrent Agent runs remain independent.
func (r *Repository) CanvasActiveAgentSummaries(ownerID, canvasID string) ([]model.CloudAgentExecution, error) {
	rows := []model.CloudAgentExecution{}
	err := r.db.Select("id", "conversation_id", "title", "status", "updated_at").Where("user_id = ? AND canvas_id = ? AND status IN ?", ownerID, canvasID, []string{"queued", "running", "waiting_approval", "waiting_member"}).Order("updated_at DESC, id").Find(&rows).Error
	return rows, err
}
func canvasLeaseNodes(payload string) (map[string]map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("canvas document is null")
	}
	out := map[string]map[string]any{}
	if doc["_deleted"] == true {
		return out, nil
	}
	nodes, _ := doc["nodes"].([]any)
	for _, raw := range nodes {
		if n, ok := raw.(map[string]any); ok {
			if id, ok := n["id"].(string); ok {
				out[id] = n
			}
		}
	}
	return out, nil
}
func canvasAuthorFields(node map[string]any) map[string]any {
	if node == nil {
		return nil
	}
	out := map[string]any{"exists": true}
	copyField := func(source map[string]any, name, path string) {
		if value, exists := source[name]; exists {
			out[path] = value
		}
	}
	copyField(node, "title", "title")
	metadata, _ := node["metadata"].(map[string]any)
	for _, key := range []string{"prompt", "composerContent"} {
		copyField(metadata, key, "metadata."+key)
	}
	spec, _ := metadata["generationSpec"].(map[string]any)
	copyField(spec, "prompt", "metadata.generationSpec.prompt")
	switch node["type"] {
	case "image", "video", "audio":
	default:
		for _, key := range []string{"content", "richText"} {
			copyField(metadata, key, "metadata."+key)
		}
	}
	return out
}

// checkCanvasAuthorLeases runs only after the account/canvas lock and compares
// actual persisted author data, regardless of caller (task, Agent or HTTP).
func (r *Repository) checkCanvasAuthorLeases(before, after *model.CanvasProject) error {
	identity := before
	if identity == nil {
		identity = after
	}
	if identity == nil {
		return nil
	}
	prior := map[string]map[string]any{}
	var err error
	if before != nil {
		prior, err = canvasLeaseNodes(before.PayloadJSON)
		if err != nil {
			return err
		}
	}
	next := map[string]map[string]any{}
	if after != nil {
		next, err = canvasLeaseNodes(after.PayloadJSON)
		if err != nil {
			return err
		}
	}
	proofs, _ := r.db.Statement.Context.Value(canvasEditProofContextKey{}).([]CanvasEditProof)
	if err := ValidateCanvasEditProofs(proofs); err != nil {
		return err
	}
	changed := []string{}
	ids := map[string]bool{}
	for id := range prior {
		ids[id] = true
	}
	for id := range next {
		ids[id] = true
	}
	for id := range ids {
		node := prior[id]
		if !reflect.DeepEqual(canvasAuthorFields(node), canvasAuthorFields(next[id])) {
			changed = append(changed, id)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	rows := []model.CanvasNodeEditLease{}
	if err := r.db.Where("owner_id = ? AND canvas_id = ? AND node_id IN ?", identity.UserID, identity.ID, changed).Find(&rows).Error; err != nil {
		return err
	}
	leases := map[string]model.CanvasNodeEditLease{}
	for _, row := range rows {
		leases[row.NodeID] = row
	}
	for _, id := range changed {
		row, exists := leases[id]
		var proof *CanvasEditProof
		for i := range proofs {
			if proofs[i].CanvasID == identity.ID && proofs[i].NodeID == id {
				proof = &proofs[i]
				break
			}
		}
		active := exists && row.ExpiresAt.After(time.Now().UTC())
		if proof != nil && (!active || !matchingLease(row, *proof)) {
			return leaseFailure("edit_proof_invalid", identity.ID, id)
		}
		if active && proof == nil {
			return leaseFailure("node_locked", identity.ID, id)
		}
	}
	return nil
}
