package repository

import (
	"encoding/json"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"time"
)

type crewExecutionIdentity struct {
	Crew *struct {
		CrewRunID   string `json:"crewRunId"`
		MemberRunID string `json:"memberRunId"`
	} `json:"crew"`
}

func (r *Repository) lockAgentCrewExecution(userID, runID string) error {
	if r.db.Dialector.Name() == "sqlite" {
		if err := r.db.Model(&model.CloudAgentExecution{}).Where("user_id = ? AND id = ?", userID, runID).UpdateColumn("revision", gorm.Expr("revision + 0")).Error; err != nil {
			return err
		}
	}
	var row model.CloudAgentExecution
	if err := r.db.Select("state_json").Where("user_id = ? AND id = ?", userID, runID).First(&row).Error; err != nil {
		return err
	}
	var context crewExecutionIdentity
	if err := json.Unmarshal([]byte(row.StateJSON), &context); err != nil || context.Crew == nil {
		return nil
	}
	q := r.db.Model(&model.AgentCrewRun{}).Where("user_id = ? AND id = ?", userID, context.Crew.CrewRunID).UpdateColumn("revision", gorm.Expr("revision + 0"))
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *Repository) projectAgentCrewExecution(execution *model.CloudAgentExecution) error {
	var context crewExecutionIdentity
	if json.Unmarshal([]byte(execution.StateJSON), &context) != nil || context.Crew == nil {
		return nil
	}
	member, err := r.AgentCrewMemberRunInternal(context.Crew.MemberRunID)
	if err != nil {
		return err
	}
	if member.Status == model.MemberRunCompleted || member.Status == model.MemberRunFailed || member.Status == model.MemberRunCancelled {
		return nil
	}
	status := member.Status
	switch execution.Status {
	case "running":
		status = model.MemberRunRunning
	case "waiting_approval":
		status = model.MemberRunWaiting
	case "failed", "rejected":
		status = model.MemberRunFailed
	case "cancelled":
		status = model.MemberRunCancelled
	case "completed":
		if member.Role == model.CrewMemberRoleCoordinator {
			status = model.MemberRunCompleted
		} else if member.ResultJSON == "" {
			status = model.MemberRunFailed
		}
	}
	if status == member.Status {
		return nil
	}
	return r.MutateAgentCrewRun(execution.UserID, context.Crew.CrewRunID, func(snapshot *model.AgentCrewRunSnapshot, tx *Repository) error {
		for i := range snapshot.Members {
			m := &snapshot.Members[i]
			if m.ID != context.Crew.MemberRunID || m.AgentRunID != execution.ID {
				continue
			}
			m.Status = status
			if status == model.MemberRunFailed {
				m.ErrorCode = "member_execution_failed"
				if execution.Status == "completed" {
					m.ErrorCode = "missing_task_result"
				}
			}
			if status == model.MemberRunCancelled {
				m.ErrorCode = "member_cancelled"
			}
			if m.Role == model.CrewMemberRoleCoordinator {
				switch m.Status {
				case model.MemberRunRunning:
					if snapshot.Run.Status == model.CrewRunQueued {
						snapshot.Run.Status = model.CrewRunRunning
					}
				case model.MemberRunFailed:
					snapshot.Run.Status = model.CrewRunFailed
				case model.MemberRunCompleted:
					if snapshot.Run.Status != model.CrewRunWaitingApproval {
						snapshot.Run.Status = model.CrewRunCompleted
					}
				}
			}
			return nil
		}
		return gorm.ErrRecordNotFound
	})
}

func (r *Repository) AppendAgentCrewEvent(run *model.AgentCrewRun, memberRunID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	run.LatestSequence++
	return r.db.Create(&model.AgentCrewEvent{CrewRunID: run.ID, Sequence: run.LatestSequence, MemberRunID: memberRunID, Type: eventType, PayloadJSON: string(raw), CreatedAt: time.Now().UTC()}).Error
}

func (r *Repository) AgentCrewEvents(userID, runID string, after int64) ([]model.CrewBusinessEvent, error) {
	if _, err := r.AgentCrewRunSnapshot(userID, runID); err != nil {
		return nil, err
	}
	var rows []model.AgentCrewEvent
	if err := r.db.Where("crew_run_id = ? AND sequence > ?", runID, after).Order("sequence asc").Limit(500).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := []model.CrewBusinessEvent{}
	for _, row := range rows {
		var payload any
		if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil {
			return nil, err
		}
		result = append(result, model.CrewBusinessEvent{CrewRunID: runID, Sequence: row.Sequence, MemberRunID: row.MemberRunID, Type: row.Type, CreatedAt: row.CreatedAt, Payload: payload})
	}
	return result, nil
}
