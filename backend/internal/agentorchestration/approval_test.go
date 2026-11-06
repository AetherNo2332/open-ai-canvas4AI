package agentorchestration

import "testing"

func TestApprovalDecisionIsFinalAndVersioned(t *testing.T) {
	pending := Approval{Status: ApprovalPending, AuthorizationVersion: 3}
	next, err := DecideApproval(pending, ApprovalApproved, 3, 3, WorkRunning)
	if err != nil || next != ApprovalApproved {
		t.Fatalf("approve: %s %v", next, err)
	}
	// Repeating the same decision is idempotent; reversing it is a conflict.
	if next, err := DecideApproval(Approval{Status: ApprovalApproved, AuthorizationVersion: 3}, ApprovalApproved, 3, 3, WorkRunning); err != nil || next != ApprovalApproved {
		t.Fatalf("repeat approve: %s %v", next, err)
	}
	if next, err := DecideApproval(Approval{Status: ApprovalConsumed, AuthorizationVersion: 3}, ApprovalApproved, 3, 3, WorkRunning); err != nil || next != ApprovalConsumed {
		t.Fatalf("approve after consume must stay consumed: %s %v", next, err)
	}
	if _, err := DecideApproval(Approval{Status: ApprovalApproved, AuthorizationVersion: 3}, ApprovalRejected, 3, 3, WorkRunning); !IsCode(err, CodeVersionConflict) {
		t.Fatalf("reverse decision: %v", err)
	}
	// The caller's view or the work moved on: the approval expires.
	if next, err := DecideApproval(pending, ApprovalApproved, 2, 3, WorkRunning); !IsCode(err, CodeVersionConflict) || next != ApprovalExpired {
		t.Fatalf("stale caller version: %s %v", next, err)
	}
	if next, err := DecideApproval(pending, ApprovalApproved, 3, 4, WorkRunning); !IsCode(err, CodeVersionConflict) || next != ApprovalExpired {
		t.Fatalf("work changed since request: %s %v", next, err)
	}
	if next, err := DecideApproval(pending, ApprovalApproved, 3, 3, WorkCancelled); !IsCode(err, CodeWorkStopped) || next != ApprovalExpired {
		t.Fatalf("stopped work: %s %v", next, err)
	}
	if _, err := DecideApproval(pending, "maybe", 3, 3, WorkRunning); !IsCode(err, CodeInvalidRequest) {
		t.Fatalf("unknown decision: %v", err)
	}
}

func TestApprovalConsumeChecksQuoteTargetAndVersion(t *testing.T) {
	approved := Approval{Status: ApprovalApproved, AuthorizationVersion: 3, QuoteMicrocredits: 100, TargetVersion: "v7"}
	if next, err := ConsumeApproval(approved, "op-1", 100, "v7", 3, WorkRunning); err != nil || next != ApprovalConsumed {
		t.Fatalf("consume: %s %v", next, err)
	}
	// B1：同一操作的重复消费幂等（崩溃后重试不重复执行），其他操作不能复用。
	if next, err := ConsumeApproval(Approval{Status: ApprovalConsumed, Operation: "op-1", AuthorizationVersion: 3, QuoteMicrocredits: 100, TargetVersion: "v7"}, "op-1", 100, "v7", 3, WorkRunning); err != nil || next != ApprovalConsumed {
		t.Fatalf("idempotent retry of the same operation: %s %v", next, err)
	}
	if _, err := ConsumeApproval(Approval{Status: ApprovalConsumed, Operation: "op-1", AuthorizationVersion: 3, QuoteMicrocredits: 100, TargetVersion: "v7"}, "op-2", 100, "v7", 3, WorkRunning); !IsCode(err, CodeVersionConflict) {
		t.Fatalf("second operation on a consumed approval: %v", err)
	}
	for name, call := range map[string]func() (string, error){
		"quote changed":  func() (string, error) { return ConsumeApproval(approved, "op-2", 150, "v7", 3, WorkRunning) },
		"target changed": func() (string, error) { return ConsumeApproval(approved, "op-1", 100, "v8", 3, WorkRunning) },
		"work changed":   func() (string, error) { return ConsumeApproval(approved, "op-1", 100, "v7", 4, WorkRunning) },
	} {
		if next, err := call(); !IsCode(err, CodeVersionConflict) || next != ApprovalExpired {
			t.Fatalf("%s: %s %v", name, next, err)
		}
	}
	if _, err := ConsumeApproval(Approval{Status: ApprovalPending}, "op-1", 0, "", 0, WorkRunning); !IsCode(err, CodeForbidden) {
		t.Fatalf("consume pending: %v", err)
	}
	if _, err := ConsumeApproval(Approval{Status: ApprovalRejected}, "op-1", 0, "", 0, WorkRunning); !IsCode(err, CodeForbidden) {
		t.Fatalf("consume rejected: %v", err)
	}
	if next, err := ConsumeApproval(approved, "op-1", 100, "v7", 3, WorkCancelled); !IsCode(err, CodeWorkStopped) || next != ApprovalExpired {
		t.Fatalf("consume on stopped work: %s %v", next, err)
	}
}
