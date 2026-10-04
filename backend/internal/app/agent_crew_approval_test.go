package app

import (
	"infinite-canvas/backend/internal/model"
	"testing"
)

func crewProposalFixture(t *testing.T) (*Service, *CrewRunView, string) {
	t.Helper()
	s, crew, _ := crewRunFixture(t)
	run, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "proposal", IdempotencyKey: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	hash := cloudAgentCanvasHash(doc)
	for i, m := range run.Members[1:] {
		id := []string{"one", "two"}[i]
		if err := s.DispatchCrewTask(run.ID, m.MemberID, CrewTaskInput{TaskID: id, Title: id, Instructions: "提出方案"}); err != nil {
			t.Fatal(err)
		}
		title := id
		result := MemberTaskResult{TaskID: id, Summary: id, Proposal: &CrewCanvasProposal{SnapshotHash: hash, Ops: []CrewCanvasOperation{{Type: "add_node", ID: "crew-" + id, NodeType: "text", Title: &title}}}}
		if err := s.CompleteMemberRun(m.ID, result); err != nil {
			t.Fatal(err)
		}
	}
	return s, run, canvas.PayloadJSON
}

func TestCrewApprovalSingleCommitAndUndo(t *testing.T) {
	s, run, before := crewProposalFixture(t)
	proposal, err := s.AggregateMemberResults(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.ApprovalID == "" || len(proposal.Ops) != 2 {
		t.Fatal("aggregate missing approval")
	}
	if _, err := s.CommitCrewProposal("user", run.ID, proposal.ApprovalID, proposal.SnapshotHash, "commit"); err == nil {
		t.Fatal("unapproved commit accepted")
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if canvas.PayloadJSON != before {
		t.Fatal("proposal modified canvas")
	}
	if err := s.DecideCrewApproval("other", run.ID, proposal.ApprovalID, "approve", ""); err == nil {
		t.Fatal("foreign approval accepted")
	}
	if err := s.DecideCrewApproval("user", run.ID, proposal.ApprovalID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitCrewProposal("user", run.ID, proposal.ApprovalID, proposal.SnapshotHash, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCrewProposal("user", run.ID, proposal.ApprovalID, proposal.SnapshotHash, "commit"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCrewProposal("user", run.ID, proposal.ApprovalID, proposal.SnapshotHash, "other-operation"); err == nil {
		t.Fatal("different operation reused approval")
	}
	mutation, err := s.repo.LatestCloudAgentCanvasMutation("user", run.CoordinatorRunID)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.StepID != "commit" {
		t.Fatal("operation identity lost")
	}
	if _, err := s.UndoCloudAgentCanvas("user", run.CoordinatorRunID, "commit", stringValue(result["snapshotHash"]), "test"); err != nil {
		t.Fatal(err)
	}
	canvas, _ = s.repo.CanvasProjectForUser("user", "agent-canvas")
	if canvas.PayloadJSON != before {
		t.Fatal("undo did not restore canvas")
	}
	view, _ := s.GetCrewRun("user", run.ID)
	if view.Status != model.CrewRunCompleted {
		t.Fatal("crew did not converge")
	}
}

func TestCrewApprovalRejectsStaleSnapshot(t *testing.T) {
	s, run, _ := crewProposalFixture(t)
	proposal, err := s.AggregateMemberResults(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecideCrewApproval("user", run.ID, proposal.ApprovalID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	canvas.Title = "User changed title"
	canvas.PayloadJSON = `{"nodes":[],"connections":[],"name":"user change"}`
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitCrewProposal("user", run.ID, proposal.ApprovalID, proposal.SnapshotHash, "stale"); err == nil {
		t.Fatal("stale proposal overwrote user changes")
	}
	current, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if current.PayloadJSON != canvas.PayloadJSON {
		t.Fatal("conflict changed canvas")
	}
}

func TestCrewCoordinatorProposalToolIsAvailable(t *testing.T) {
	request := CloudAgentRequest{PermissionMode: "read_only", crew: &CrewMemberRuntime{Role: model.CrewMemberRoleCoordinator, Permission: model.CrewPermissionPropose}}
	if !cloudAgentToolAllowed(request, "crew_propose") {
		t.Fatal("coordinator cannot request aggregate approval")
	}
}

func TestCrewAggregateRejectsConflictingMemberProposals(t *testing.T) {
	s, crew, _ := crewRunFixture(t)
	run, err := s.CreateCrewRun("user", crew.ID, CreateCrewRunInput{Prompt: "conflict", IdempotencyKey: "conflict"})
	if err != nil {
		t.Fatal(err)
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	hash := cloudAgentCanvasHash(doc)
	for i, m := range run.Members[1:] {
		id := []string{"a", "b"}[i]
		title := id
		if err := s.DispatchCrewTask(run.ID, m.MemberID, CrewTaskInput{TaskID: id, Title: id, Instructions: "propose"}); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteMemberRun(m.ID, MemberTaskResult{TaskID: id, Summary: id, Proposal: &CrewCanvasProposal{SnapshotHash: hash, Ops: []CrewCanvasOperation{{Type: "add_node", ID: "shared", NodeType: "text", Title: &title}}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AggregateMemberResults(run.ID); err == nil {
		t.Fatal("conflicting proposals accepted")
	}
	current, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if current.PayloadJSON != canvas.PayloadJSON {
		t.Fatal("conflict modified canvas")
	}
}
