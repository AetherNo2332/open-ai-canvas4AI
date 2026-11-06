package creationplatform

import "slices"

// BudgetUsage is the money a grant has spent or holds, in microcredits.
// InFlight and PendingReview are held at the task's maximum possible charge
// until the order settles; Refunded is informational and holds nothing.
type BudgetUsage struct {
	Charged       int64 `json:"chargedMicrocredits"`
	InFlight      int64 `json:"inFlightMicrocredits"`
	PendingReview int64 `json:"pendingReviewMicrocredits"`
	Refunded      int64 `json:"refundedMicrocredits"`
}

func (u BudgetUsage) add(other BudgetUsage) BudgetUsage {
	return BudgetUsage{Charged: u.Charged + other.Charged, InFlight: u.InFlight + other.InFlight, PendingReview: u.PendingReview + other.PendingReview, Refunded: u.Refunded + other.Refunded}
}

// Spent is what the usage takes from a budget: settled charges plus holds.
func (u BudgetUsage) Spent() int64 { return u.Charged + u.InFlight + u.PendingReview }

// BudgetNode is one grant of a tree as stored. Direct is the usage of orders
// admitted against this grant itself, not against its children.
type BudgetNode struct {
	ID       string
	ParentID string
	Limit    int64
	Open     bool
	Direct   BudgetUsage
}

// BudgetTree evaluates one grant tree. A budget is not a wallet: it never
// moves credits; it only decides whether a new allocation or charge fits.
type BudgetTree struct {
	nodes    map[string]BudgetNode
	children map[string][]string
	root     string
}

// NewBudgetTree requires exactly one root and every parent to exist; a grant
// has one parent, so a valid tree cannot share an allocation between parents.
func NewBudgetTree(nodes []BudgetNode) (*BudgetTree, error) {
	tree := &BudgetTree{nodes: map[string]BudgetNode{}, children: map[string][]string{}}
	for _, node := range nodes {
		if node.ID == "" || node.Limit < 0 {
			return nil, NewError(CodeInvalidRequest, "预算授权缺少 ID 或额度无效")
		}
		if _, exists := tree.nodes[node.ID]; exists {
			return nil, NewError(CodeInvalidRequest, "预算授权重复")
		}
		tree.nodes[node.ID] = node
		if node.ParentID == "" {
			if tree.root != "" {
				return nil, NewError(CodeInvalidRequest, "预算树只能有一个根授权")
			}
			tree.root = node.ID
		}
	}
	if tree.root == "" {
		return nil, NewError(CodeInvalidRequest, "预算树缺少根授权")
	}
	for _, node := range nodes {
		if node.ParentID == "" {
			continue
		}
		if _, ok := tree.nodes[node.ParentID]; !ok {
			return nil, NewError(CodeInvalidRequest, "预算授权的上级不存在")
		}
		tree.children[node.ParentID] = append(tree.children[node.ParentID], node.ID)
	}
	if len(tree.Descendants(tree.root))+1 != len(tree.nodes) {
		return nil, NewError(CodeInvalidRequest, "预算树存在环或孤立授权")
	}
	return tree, nil
}

// Descendants lists every grant under id, breadth first.
func (t *BudgetTree) Descendants(id string) []string {
	out := []string{}
	queue := slices.Clone(t.children[id])
	seen := map[string]bool{id: true}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		out = append(out, next)
		queue = append(queue, t.children[next]...)
	}
	return out
}

// Subtree is the usage of a grant and all grants under it; each order is
// counted once, on the grant it was admitted against.
func (t *BudgetTree) Subtree(id string) BudgetUsage {
	usage := t.nodes[id].Direct
	for _, child := range t.Descendants(id) {
		usage = usage.add(t.nodes[child].Direct)
	}
	return usage
}

// occupied is what a child takes from its parent: its whole limit while open,
// and only what it actually spent or still holds once closed.
func (t *BudgetTree) occupied(child string) int64 {
	node := t.nodes[child]
	if node.Open {
		return node.Limit
	}
	return t.Subtree(child).Spent()
}

// Available is what a grant can still allocate to a new child or charge.
func (t *BudgetTree) Available(id string) int64 {
	node, ok := t.nodes[id]
	if !ok {
		return 0
	}
	available := node.Limit - node.Direct.Spent()
	for _, child := range t.children[id] {
		available -= t.occupied(child)
	}
	return max(available, 0)
}

func (t *BudgetTree) requireOpen(id string) error {
	node, ok := t.nodes[id]
	if !ok {
		return NewError(CodeNotFound, "预算授权不存在")
	}
	for {
		if !node.Open {
			return NewError(CodeBudgetClosed, "预算授权已关闭，不能再分配或收费")
		}
		if node.ParentID == "" {
			return nil
		}
		node = t.nodes[node.ParentID]
	}
}

// AdmitAllocation checks that a new child of the given limit fits.
func (t *BudgetTree) AdmitAllocation(parentID string, limit int64) error {
	if limit <= 0 {
		return NewError(CodeInvalidRequest, "分配额度必须大于 0")
	}
	return t.admit(parentID, limit)
}

// AdmitCharge checks that a new order holding amount fits. Zero-amount (free)
// tasks only need every grant on the path to be open.
func (t *BudgetTree) AdmitCharge(grantID string, amount int64) error {
	if amount < 0 {
		return NewError(CodeInvalidRequest, "费用不能为负")
	}
	return t.admit(grantID, amount)
}

func (t *BudgetTree) admit(id string, amount int64) error {
	if err := t.requireOpen(id); err != nil {
		return err
	}
	if amount > t.Available(id) {
		return NewError(CodeBudgetExceeded, "超出预算授权的剩余额度")
	}
	return nil
}
