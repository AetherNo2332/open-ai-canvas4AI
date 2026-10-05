package app

import "infinite-canvas/backend/internal/model"

func (s *Service) ListCrewRuns(userID, canvasID string) ([]CrewRunView, error) {
	if _, err := s.repo.CanvasProjectForUser(userID, canvasID); err != nil {
		return nil, crewPublicError(err)
	}
	rows, err := s.repo.AgentCrewRuns(userID, canvasID)
	if err != nil {
		return nil, err
	}
	views := []CrewRunView{}
	for _, row := range rows {
		view, err := s.GetCrewRun(userID, row.ID)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func (s *Service) CrewRunEvents(userID, runID string, after int64) ([]model.CrewBusinessEvent, error) {
	if after < 0 {
		return nil, BadAuthRequest("Crew 事件游标无效")
	}
	if _, err := s.GetCrewRun(userID, runID); err != nil {
		return nil, err
	}
	return s.repo.AgentCrewEvents(userID, runID, after)
}
