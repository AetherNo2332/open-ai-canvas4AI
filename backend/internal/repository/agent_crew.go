package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

func loadAgentCrew(tx *gorm.DB, userID, crewID string, lock bool) (*model.AgentCrewSnapshot, error) {
	snapshot := &model.AgentCrewSnapshot{}
	query := tx.Where("id = ? AND user_id = ?", crewID, userID)
	if lock && tx.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	if err := query.First(&snapshot.Crew).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("crew_id = ?", crewID).Order("position asc, id asc").Find(&snapshot.Members).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("member_id IN (?)", tx.Model(&model.AgentCrewMember{}).Select("id").Where("crew_id = ?", crewID)).Order("position asc, skill_id asc").Find(&snapshot.Skills).Error; err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (r *Repository) AgentCrewSnapshot(userID, crewID string) (*model.AgentCrewSnapshot, error) {
	var snapshot *model.AgentCrewSnapshot
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = loadAgentCrew(tx, userID, crewID, true)
		return err
	})
	return snapshot, err
}

func (r *Repository) AgentCrewIDs(userID, workspaceID string) ([]string, error) {
	ids := []string{}
	err := r.db.Model(&model.AgentCrew{}).Where("user_id = ? AND workspace_id = ?", userID, workspaceID).Order("created_at asc, id asc").Pluck("id", &ids).Error
	return ids, err
}

func (r *Repository) AgentCrewIDForMember(userID, memberID string) (string, error) {
	var member model.AgentCrewMember
	err := r.db.Joins("JOIN agent_crews ON agent_crews.id = agent_crew_members.crew_id").Where("agent_crew_members.id = ? AND agent_crews.user_id = ?", memberID, userID).First(&member).Error
	return member.CrewID, err
}

func saveCrewChildren(tx *gorm.DB, snapshot *model.AgentCrewSnapshot) error {
	if err := tx.Where("member_id IN (?)", tx.Model(&model.AgentCrewMember{}).Select("id").Where("crew_id = ?", snapshot.Crew.ID)).Delete(&model.AgentCrewMemberSkill{}).Error; err != nil {
		return err
	}
	if err := tx.Where("crew_id = ?", snapshot.Crew.ID).Delete(&model.AgentCrewMember{}).Error; err != nil {
		return err
	}
	if len(snapshot.Members) > 0 {
		if err := tx.Create(&snapshot.Members).Error; err != nil {
			return err
		}
	}
	if len(snapshot.Skills) > 0 {
		return tx.Create(&snapshot.Skills).Error
	}
	return nil
}

func (r *Repository) CreateAgentCrew(snapshot *model.AgentCrewSnapshot) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var workspace model.AgentWorkspace
		if err := tx.Where("id = ? AND user_id = ?", snapshot.Crew.WorkspaceID, snapshot.Crew.UserID).First(&workspace).Error; err != nil {
			return err
		}
		if err := tx.Create(&snapshot.Crew).Error; err != nil {
			return err
		}
		return saveCrewChildren(tx, snapshot)
	})
}

func lockAgentCrew(tx *gorm.DB, userID, crewID string, revision int64) (*model.AgentCrewSnapshot, error) {
	if err := tx.Model(&model.AgentCrew{}).Where("id = ? AND user_id = ?", crewID, userID).UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
		return nil, err
	}
	snapshot, err := loadAgentCrew(tx, userID, crewID, false)
	if err != nil {
		return nil, err
	}
	if snapshot.Crew.Revision != revision {
		return nil, kernel.AgentCrewConflict(snapshot.Crew.Revision)
	}
	return snapshot, nil
}

// The callback validates the complete locked aggregate, so Coordinator changes
// and member/skill replacement are never visible as intermediate configurations.
func (r *Repository) MutateAgentCrew(userID, crewID string, revision int64, mutate func(*model.AgentCrewSnapshot) error) (int64, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		snapshot, err := lockAgentCrew(tx, userID, crewID, revision)
		if err != nil {
			return err
		}
		if err := mutate(snapshot); err != nil {
			return err
		}
		snapshot.Crew.Revision = revision + 1
		if err := tx.Save(&snapshot.Crew).Error; err != nil {
			return err
		}
		return saveCrewChildren(tx, snapshot)
	})
	return revision + 1, err
}

func (r *Repository) DeleteAgentCrew(userID, crewID string, revision int64) (int64, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		snapshot, err := lockAgentCrew(tx, userID, crewID, revision)
		if err != nil {
			return err
		}
		if err := tx.Where("member_id IN (?)", tx.Model(&model.AgentCrewMember{}).Select("id").Where("crew_id = ?", crewID)).Delete(&model.AgentCrewMemberSkill{}).Error; err != nil {
			return err
		}
		if err := tx.Where("crew_id = ?", crewID).Delete(&model.AgentCrewMember{}).Error; err != nil {
			return err
		}
		return tx.Delete(&snapshot.Crew).Error
	})
	return revision + 1, err
}
