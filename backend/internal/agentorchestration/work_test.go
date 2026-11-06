package agentorchestration

import "testing"

func TestWorkTransitionsAreExplicit(t *testing.T) {
	allowed := map[[2]string]bool{
		{WorkPending, WorkRunning}: true, {WorkRunning, WorkWaitingUser}: true, {WorkWaitingUser, WorkRunning}: true,
		{WorkRunning, WorkPaused}: true, {WorkPending, WorkPaused}: true, {WorkPaused, WorkRunning}: true,
		{WorkRunning, WorkCompleted}: true, {WorkRunning, WorkFailed}: true,
		{WorkPending, WorkCancelled}: true, {WorkRunning, WorkCancelled}: true, {WorkPaused, WorkCancelled}: true, {WorkWaitingUser, WorkCancelled}: true,
	}
	states := []string{WorkPending, WorkRunning, WorkWaitingUser, WorkPaused, WorkCompleted, WorkFailed, WorkCancelled}
	for _, from := range states {
		for _, to := range states {
			err := CheckWorkTransition(from, to)
			if allowed[[2]string{from, to}] != (err == nil) {
				t.Fatalf("%s -> %s: allowed=%v err=%v", from, to, allowed[[2]string{from, to}], err)
			}
		}
	}
}

func TestStoppedWorkNeverRevives(t *testing.T) {
	for _, terminal := range []string{WorkCompleted, WorkFailed, WorkCancelled} {
		if !WorkTerminal(terminal) {
			t.Fatalf("%s must be terminal", terminal)
		}
		if err := CheckDelegation(terminal); !IsCode(err, CodeWorkStopped) {
			t.Fatalf("delegation on %s: %v", terminal, err)
		}
	}
	if err := CheckDelegation(WorkPaused); !IsCode(err, CodeWorkStopped) {
		t.Fatalf("delegation on paused work: %v", err)
	}
	if err := CheckDelegation(WorkPending); err != nil {
		t.Fatalf("delegation on pending work: %v", err)
	}
}

func TestReportApplicationFencesStaleAssignments(t *testing.T) {
	current := Assignment{ID: "as-2", Epoch: 2, Status: AssignmentRunning}
	cases := []struct {
		name   string
		report Report
		work   int64
		code   ErrorCode
	}{
		{"older assignment", Report{AssignmentID: "as-1", LeaseEpoch: 1, WorkVersion: 3, Status: ReportCompleted}, 3, CodeStaleAssignment},
		{"older epoch", Report{AssignmentID: "as-2", LeaseEpoch: 1, WorkVersion: 3, Status: ReportCompleted}, 3, CodeStaleAssignment},
		{"older work version", Report{AssignmentID: "as-2", LeaseEpoch: 2, WorkVersion: 2, Status: ReportCompleted}, 3, CodeStaleAssignment},
		{"unknown status", Report{AssignmentID: "as-2", LeaseEpoch: 2, WorkVersion: 3, Status: "done"}, 3, CodeInvalidRequest},
	}
	for _, tc := range cases {
		if _, err := ApplyReport(current, tc.work, tc.report); !IsCode(err, tc.code) {
			t.Fatalf("%s: want %s, got %v", tc.name, tc.code, err)
		}
	}
	effect, err := ApplyReport(current, 3, Report{AssignmentID: "as-2", LeaseEpoch: 2, WorkVersion: 3, Status: ReportCompleted})
	if err != nil || effect.WorkStatus != WorkCompleted || !effect.EndAssignment {
		t.Fatalf("completed report: %+v %v", effect, err)
	}
	effect, err = ApplyReport(current, 3, Report{AssignmentID: "as-2", LeaseEpoch: 2, WorkVersion: 3, Status: ReportProgress})
	if err != nil || effect.WorkStatus != "" || effect.EndAssignment {
		t.Fatalf("progress report must not change status: %+v %v", effect, err)
	}
	effect, err = ApplyReport(current, 3, Report{AssignmentID: "as-2", LeaseEpoch: 2, WorkVersion: 3, Status: ReportNeedsUser})
	if err != nil || effect.WorkStatus != WorkWaitingUser {
		t.Fatalf("needs-user report: %+v %v", effect, err)
	}
	ended := current
	ended.Status = AssignmentEnded
	if _, err := ApplyReport(ended, 3, Report{AssignmentID: "as-2", LeaseEpoch: 2, WorkVersion: 3, Status: ReportCompleted}); !IsCode(err, CodeStaleAssignment) {
		t.Fatalf("report on ended assignment: %v", err)
	}
}

func TestDelegationActionKeyIsStable(t *testing.T) {
	a := DelegationKey("u1", "msg-1", "drama-storyboard")
	if a != DelegationKey("u1", "msg-1", "drama-storyboard") || a == DelegationKey("u1", "msg-1", "ecommerce") || a == DelegationKey("u2", "msg-1", "drama-storyboard") {
		t.Fatal("delegation key must be stable per owner + source message + action key")
	}
}
