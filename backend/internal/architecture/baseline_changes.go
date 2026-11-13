package architecture

import (
	"fmt"
	"strings"
)

// BaselineMove is a reviewable, one-revision relocation of existing debt.
type BaselineMove struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

func allowanceIndex(b Baseline) map[string]Allowance {
	out := map[string]Allowance{}
	for _, a := range b.Allowances {
		out[a.ID] = a
	}
	return out
}

func baselineFinding(id, detail string, value, limit int) Finding {
	return Finding{ID: id, Rule: "baseline_change", Detail: detail, Value: value, Limit: limit}
}

// CompareBaselines rejects additions/increases even when the code and its
// allowance change together. A relocation needs independent scan evidence too.
func CompareBaselines(previous Baseline, current Baseline, moves []BaselineMove) []Finding {
	out := []Finding{}
	if previous.Version != 1 || current.Version != 1 {
		return []Finding{baselineFinding("baseline", "both baseline versions must be 1", 0, 1)}
	}
	old, next := allowanceIndex(previous), allowanceIndex(current)
	sources, targets := map[string]bool{}, map[string]bool{}
	for _, m := range moves {
		sources[m.From] = true
		targets[m.To] = true
	}
	mapped := map[string]Allowance{}
	seenFrom, seenTo := map[string]bool{}, map[string]bool{}
	for _, m := range moves {
		a, ok := old[m.From]
		b, exists := next[m.To]
		_, sourceRemains := next[m.From]
		_, targetExisted := old[m.To]
		invalid := invalidMove(m, a, b) || !ok || !exists || sourceRemains || targetExisted
		invalid = invalid || seenFrom[m.From] || seenTo[m.To] || sources[m.To] || targets[m.From]
		if invalid {
			out = append(out, baselineFinding(m.To, "invalid move from "+m.From+": one-to-one, same rule, no growth; source must be removed", b.Max, a.Max))
		} else {
			mapped[m.To] = a
		}
		seenFrom[m.From] = true
		seenTo[m.To] = true
	}
	for _, b := range current.Allowances {
		a, ok := old[b.ID]
		if !ok {
			a, ok = mapped[b.ID]
		}
		if !ok || b.Max > a.Max {
			out = append(out, baselineFinding(b.ID, "new or increased allowance", b.Max, a.Max))
		}
	}
	return out
}

func invalidMove(m BaselineMove, a, b Allowance) bool {
	return m.From == m.To || strings.TrimSpace(m.Reason) == "" || ruleSuffix(m.From) != ruleSuffix(m.To) || b.Max > a.Max || ruleSuffix(m.To) == "dependency"
}

func ruleSuffix(id string) string {
	if strings.Contains(id, ":signature:") {
		return "untyped_protocol_map"
	}
	if strings.Contains(id, ":import:") {
		return "dependency"
	}
	parts := strings.Split(id, ":")
	return parts[len(parts)-1]
}

// CheckBaselineMoves verifies declared moves against both source revisions.
// This cannot establish semantic equivalence; that remains a review obligation.
func CheckBaselineMoves(previous, current Baseline, moves []BaselineMove, before, after []Finding) []Finding {
	out := CompareBaselines(previous, current, moves)
	old, now := map[string]Finding{}, map[string]Finding{}
	for _, f := range before {
		old[f.ID] = f
	}
	for _, f := range after {
		now[f.ID] = f
	}
	for _, m := range moves {
		a, ok := old[m.From]
		b, exists := now[m.To]
		_, remains := now[m.From]
		if !ok || !exists || remains || a.Rule != b.Rule || b.Value > a.Value {
			out = append(out, baselineFinding(m.To, fmt.Sprintf("move evidence from %s missing, retained, changed rule or increased", m.From), b.Value, a.Value))
		}
	}
	return out
}
