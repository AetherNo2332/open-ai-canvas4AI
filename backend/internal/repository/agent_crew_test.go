package repository

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestAgentCrewRepositoryCASOwnershipAndDelete(t *testing.T) {
	r := newWorkspaceRepository(t)
	if err := r.db.AutoMigrate(&model.AgentCrew{}, &model.AgentCrewMember{}, &model.AgentCrewMemberSkill{}); err != nil {
		t.Fatal(err)
	}
	workspace, err := r.AgentWorkspaceSnapshot("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	group := model.AgentCrewSnapshot{Crew: model.AgentCrew{ID: "crew", UserID: "user", WorkspaceID: workspace.ID, Name: "Original", Status: "enabled", CoordinatorMemberID: "director"}, Members: []model.AgentCrewMember{{ID: "director", CrewID: "crew", Role: model.CrewMemberRoleCoordinator, Enabled: true}}}
	if err := r.CreateAgentCrew(&group); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AgentCrewSnapshot("other", "crew"); err == nil {
		t.Fatal("foreign read allowed")
	}
	_, err = r.MutateAgentCrew("user", "crew", 0, func(snapshot *model.AgentCrewSnapshot) error { snapshot.Crew.Name = "Updated"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.MutateAgentCrew("user", "crew", 0, func(snapshot *model.AgentCrewSnapshot) error { snapshot.Crew.Name = "Lost"; return nil }); err == nil {
		t.Fatal("stale write accepted")
	}
	snapshot, err := r.AgentCrewSnapshot("user", "crew")
	if err != nil || snapshot.Crew.Name != "Updated" || snapshot.Crew.Revision != 1 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	if _, err := r.DeleteAgentCrew("other", "crew", 1); err == nil {
		t.Fatal("foreign delete")
	}
	if _, err := r.DeleteAgentCrew("user", "crew", 1); err != nil {
		t.Fatal(err)
	}
	var members int64
	r.db.Model(&model.AgentCrewMember{}).Count(&members)
	if members != 0 {
		t.Fatal("orphan member")
	}
}
