package app

import (
	"errors"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

func crewService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	s, db := workspaceService(t)
	if err := db.AutoMigrate(&model.AgentCrew{}, &model.AgentCrewMember{}, &model.AgentCrewMemberSkill{}); err != nil {
		t.Fatal(err)
	}
	return s, db
}
func crewMember(name string, position int) CrewMemberInput {
	return CrewMemberInput{Name: name, Role: model.CrewMemberRoleMember, Model: CrewModelConfig{Model: "text-model"}, Permission: model.CrewPermissionPropose, Enabled: true, Position: position, Budget: CrewMemberBudget{MaxCredits: 10, MaxSteps: 20}}
}
func TestAgentCrewCoordinatorCASAndIsolation(t *testing.T) {
	s, _ := crewService(t)
	coordinator := crewMember("导演", 0)
	coordinator.Role = model.CrewMemberRoleCoordinator
	crew, err := s.CreateCrew("user", "workspace-canvas", CreateCrewInput{Name: "剧组", Status: "enabled", Members: []CrewMemberInput{coordinator, crewMember("编剧", 1)}})
	if err != nil {
		t.Fatal(err)
	}
	if crew.Revision != 0 || len(crew.Members) != 2 || crew.CoordinatorMemberID != crew.Members[0].ID {
		t.Fatalf("crew: %+v", crew)
	}
	if _, err := s.GetCrew("other", crew.ID); err == nil {
		t.Fatal("cross-account crew exposed")
	}
	if _, err := s.UpdateCrew("other", crew.ID, 0, UpdateCrewInput{Name: "stolen", Status: "enabled"}); err == nil {
		t.Fatal("cross-account update")
	}
	disabled := coordinator
	disabled.Enabled = false
	if _, err := s.UpdateCrewMember("user", crew.Members[0].ID, 0, disabled); err == nil {
		t.Fatal("disabled sole coordinator")
	}
	duplicate := coordinator
	duplicate.Position = 2
	if _, err := s.AddCrewMember("user", crew.ID, 0, duplicate); err == nil {
		t.Fatal("second coordinator")
	}
	duplicate = crewMember("同序号", 1)
	if _, err := s.AddCrewMember("user", crew.ID, 0, duplicate); err == nil {
		t.Fatal("duplicate position")
	}
	switched, err := s.UpdateCrew("user", crew.ID, 0, UpdateCrewInput{Name: "剧组", Status: "enabled", CoordinatorMemberID: crew.Members[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if switched.Revision != 1 || switched.Members[0].Role != model.CrewMemberRoleMember || switched.Members[1].Role != model.CrewMemberRoleCoordinator {
		t.Fatal("coordinator swap was not atomic")
	}
	_, err = s.AddCrewMember("user", crew.ID, 0, crewMember("过期", 2))
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Reason != "agent_crew_revision_conflict" {
		t.Fatalf("stale CAS: %v", err)
	}
	if _, err := s.DeleteCrewMember("user", switched.Members[1].ID, 1); err == nil {
		t.Fatal("removed enabled coordinator")
	}
	if _, err := s.DeleteCrew("user", crew.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCrew("user", crew.ID); err == nil {
		t.Fatal("deleted crew still visible")
	}
}

func TestAgentCrewMemberPermissionsBudgetFocusAndSkills(t *testing.T) {
	s, db := crewService(t)
	coordinator := crewMember("导演", 0)
	coordinator.Role = model.CrewMemberRoleCoordinator
	for _, mutate := range []func(*CrewMemberInput){
		func(m *CrewMemberInput) { m.Permission = model.CrewPermissionWrite },
		func(m *CrewMemberInput) { m.Budget.MaxCredits = -1 },
		func(m *CrewMemberInput) { m.Budget.MaxSteps = 10000 },
		func(m *CrewMemberInput) { m.FocusNodeIDs = []string{"foreign-node"} },
		func(m *CrewMemberInput) { m.Model = CrewModelConfig{} },
	} {
		bad := coordinator
		mutate(&bad)
		if _, err := s.CreateCrew("user", "workspace-canvas", CreateCrewInput{Name: "invalid", Status: "enabled", Members: []CrewMemberInput{bad}}); err == nil {
			t.Fatalf("invalid configuration accepted: %+v", bad)
		}
	}
	crew, err := s.CreateCrew("user", "workspace-canvas", CreateCrewInput{Name: "剧组", Status: "enabled", Members: []CrewMemberInput{coordinator}})
	if err != nil {
		t.Fatal(err)
	}
	seedSelectionSkill(t, s, db, "public", 10, false, 1)
	seedSelectionSkillFor(t, s, db, "other", "private", 10, true, 1)
	memberID := crew.Members[0].ID
	for _, selection := range []SkillSelection{{SkillID: "public", SkillVersionID: "missing", Enabled: true}, {SkillID: "private", SkillVersionID: "private-v1", Enabled: true}} {
		if _, err := s.ReplaceCrewMemberSkills("user", memberID, 0, []SkillSelection{selection}); err == nil {
			t.Fatal("invalid skill accepted")
		}
	}
	crew, err = s.ReplaceCrewMemberSkills("user", memberID, 0, []SkillSelection{{SkillID: "public", SkillVersionID: "public-v1", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if crew.Revision != 1 || len(crew.Members[0].Skills) != 1 || crew.Members[0].Skills[0].Source != "crew_member" || crew.Members[0].Skills[0].ContentHash == "" {
		t.Fatalf("member skill: %+v", crew.Members[0])
	}
	if _, err := s.ReplaceCrewMemberSkills("other", memberID, 1, nil); err == nil {
		t.Fatal("cross-account member skill update")
	}
}
