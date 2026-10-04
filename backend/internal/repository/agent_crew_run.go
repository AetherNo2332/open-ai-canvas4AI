package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
)

type CloudAgentAdmission struct {
	Execution *model.CloudAgentExecution
	Task      *model.Task
	Order     *model.BillingOrder
	Skills    []model.AgentConversationSkill
}

func (r *Repository) AgentCrewRuns(userID, canvasID string) ([]model.AgentCrewRun, error) {
	rows := []model.AgentCrewRun{}
	err := r.db.Where("user_id = ? AND canvas_id = ?", userID, canvasID).Order("created_at desc, id desc").Limit(20).Find(&rows).Error
	return rows, err
}

func (r *Repository) AllAgentCrewRuns() ([]model.AgentCrewRun, error) {
	rows := []model.AgentCrewRun{}
	err := r.db.Find(&rows).Error
	return rows, err
}

func (r *Repository) AgentCrewRunSnapshot(userID, runID string) (*model.AgentCrewRunSnapshot, error) {
	snapshot := &model.AgentCrewRunSnapshot{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Where("user_id = ? AND id = ?", userID, runID)
		if tx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "SHARE"})
		}
		if err := query.First(&snapshot.Run).Error; err != nil {
			return err
		}
		return tx.Where("crew_run_id = ?", runID).Order("position asc, attempt asc, id asc").Find(&snapshot.Members).Error
	})
	return snapshot, err
}

// Run configurations, holding reservations and independent Pi sessions commit
// together. A later member admission failure leaves no partial Crew behind.
func (r *Repository) CreateAgentCrewRunBundle(snapshot *model.AgentCrewRunSnapshot, admissions []CloudAgentAdmission, activeTaskLimit int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&snapshot.Run).Error; err != nil {
			return err
		}
		if err := New(tx).AppendAgentCrewEvent(&snapshot.Run, "", model.CrewEventRunCreated, map[string]any{"status": snapshot.Run.Status}); err != nil {
			return err
		}
		if err := tx.Model(&snapshot.Run).UpdateColumn("latest_sequence", snapshot.Run.LatestSequence).Error; err != nil {
			return err
		}
		for _, admission := range admissions {
			if err := createCloudAgentHoldingTask(tx, admission.Task, admission.Order, admission.Execution, activeTaskLimit, admission.Skills); err != nil {
				return err
			}
		}
		if len(snapshot.Members) > 0 {
			return tx.Create(&snapshot.Members).Error
		}
		return nil
	})
}

// Updating the aggregate first serializes member transitions on SQLite and Postgres.
func (r *Repository) MutateAgentCrewRun(userID, runID string, fn func(*model.AgentCrewRunSnapshot, *Repository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&model.AgentCrewRun{}).Where("user_id = ? AND id = ?", userID, runID).UpdateColumn("revision", gorm.Expr("revision + 1"))
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		repo := New(tx)
		repo.crewMutation = true
		snapshot, err := repo.AgentCrewRunSnapshot(userID, runID)
		if err != nil {
			return err
		}
		previousStatus := snapshot.Run.Status
		previousMembers := map[string]model.AgentCrewMemberRun{}
		for _, m := range snapshot.Members {
			previousMembers[m.ID] = m
		}
		if err := fn(snapshot, repo); err != nil {
			return err
		}
		for _, m := range snapshot.Members {
			old := previousMembers[m.ID]
			eventType := ""
			if m.TaskID != old.TaskID {
				eventType = model.CrewEventMemberMessage
			}
			if m.Status != old.Status {
				switch m.Status {
				case model.MemberRunRunning:
					eventType = model.CrewEventMemberRunStarted
				case model.MemberRunCompleted:
					eventType = model.CrewEventMemberRunCompleted
				case model.MemberRunFailed, model.MemberRunCancelled:
					eventType = model.CrewEventMemberRunFailed
				case model.MemberRunWaiting:
					eventType = model.CrewEventMemberRunWaiting
				}
			}
			if eventType != "" {
				if err := repo.AppendAgentCrewEvent(&snapshot.Run, m.ID, eventType, map[string]any{"memberId": m.MemberID, "name": m.Name, "role": m.Role, "status": m.Status, "attempt": m.Attempt, "taskId": m.TaskID, "summary": m.Summary, "errorCode": m.ErrorCode}); err != nil {
					return err
				}
			}
		}
		if snapshot.Run.Status != previousStatus {
			eventType := ""
			switch snapshot.Run.Status {
			case model.CrewRunWaitingApproval:
				eventType = model.CrewEventApprovalRequired
			case model.CrewRunCompleted, model.CrewRunFailed, model.CrewRunCancelled:
				eventType = model.CrewEventCrewCompleted
			}
			if eventType != "" {
				if err := repo.AppendAgentCrewEvent(&snapshot.Run, "", eventType, map[string]any{"status": snapshot.Run.Status, "approvalId": snapshot.Run.ApprovalID}); err != nil {
					return err
				}
			}
		}
		if err := tx.Save(&snapshot.Run).Error; err != nil {
			return err
		}
		for i := range snapshot.Members {
			if err := tx.Save(&snapshot.Members[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) AgentCrewRunInternal(runID string) (*model.AgentCrewRun, error) {
	row := &model.AgentCrewRun{}
	err := r.db.Where("id = ?", runID).First(row).Error
	return row, err
}

func (r *Repository) AgentCrewMemberRunInternal(memberRunID string) (*model.AgentCrewMemberRun, error) {
	row := &model.AgentCrewMemberRun{}
	err := r.db.Where("id = ?", memberRunID).First(row).Error
	return row, err
}

func (r *Repository) AppendAgentCrewMessage(message *model.AgentCrewMessage) error {
	return r.db.Create(message).Error
}
