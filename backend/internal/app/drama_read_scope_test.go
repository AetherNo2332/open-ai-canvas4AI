package app

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestDramaReadKeepsOwnerScopeAndScopedErrors(t *testing.T) {
	svc, fake := newDramaTestService(t)
	own := createTestDrama(t, svc, DramaReviewAuto)

	foreignProject := model.Project{
		ID: "project-foreign", UserID: "usr-other", Name: "他人短剧", Status: model.ProjectStatusActive,
	}
	foreignProduction := model.DramaProduction{
		ProjectID: foreignProject.ID, UserID: foreignProject.UserID,
		Status: model.DramaStatusDraft, Stage: model.DramaStageBible, SettingsJSON: "{}",
	}
	if err := fake.db.Create(&foreignProject).Error; err != nil {
		t.Fatal(err)
	}
	if err := fake.db.Create(&foreignProduction).Error; err != nil {
		t.Fatal(err)
	}
	ownerEpisode := model.DramaEpisode{
		ID: "episode-owner", ProjectID: own.Project.ID, Number: 99, Title: "作用域测试",
		Stage: model.DramaEpisodeScript, ChapterIDsJSON: "[]",
	}
	if err := fake.db.Create(&ownerEpisode).Error; err != nil {
		t.Fatal(err)
	}

	ownerList, err := svc.ListDramas("usr-drama", DramaListRequest{Page: 1, PageSize: 24})
	if err != nil {
		t.Fatal(err)
	}
	ownerItems := ownerList["items"].([]DramaListItem)
	if len(ownerItems) != 1 || ownerItems[0].Project.ID != own.Project.ID {
		t.Fatalf("owner list leaked or lost projects: %+v", ownerItems)
	}

	foreignList, err := svc.ListDramas("usr-other", DramaListRequest{Page: 1, PageSize: 24})
	if err != nil {
		t.Fatal(err)
	}
	foreignItems := foreignList["items"].([]DramaListItem)
	if len(foreignItems) != 1 || foreignItems[0].Project.ID != foreignProject.ID {
		t.Fatalf("foreign list = %+v", foreignItems)
	}
	if foreignList["total"].(int64) != 1 || foreignList["statusCounts"].(map[string]int64)["all"] != 1 {
		t.Fatalf("foreign list scope/counts = %+v", foreignList)
	}

	foreignSummaryErr := requireDramaScopeError(t, func() error {
		_, err := svc.DramaSummary("usr-other", own.Project.ID)
		return err
	})
	missingSummaryErr := requireDramaScopeError(t, func() error {
		_, err := svc.DramaSummary("usr-other", "project-missing")
		return err
	})
	if foreignSummaryErr.Error() != missingSummaryErr.Error() {
		t.Fatalf("summary errors expose ownership: foreign=%q missing=%q", foreignSummaryErr, missingSummaryErr)
	}

	episodeID := ownerEpisode.ID
	foreignDetailErr := requireDramaScopeError(t, func() error {
		_, err := svc.DramaEpisodeDetail("usr-other", own.Project.ID, episodeID)
		return err
	})
	missingDetailErr := requireDramaScopeError(t, func() error {
		_, err := svc.DramaEpisodeDetail("usr-other", "project-missing", episodeID)
		return err
	})
	if foreignDetailErr.Error() != missingDetailErr.Error() {
		t.Fatalf("detail errors expose ownership: foreign=%q missing=%q", foreignDetailErr, missingDetailErr)
	}
}

func requireDramaScopeError(t *testing.T, call func() error) error {
	t.Helper()
	err := call()
	if err == nil {
		t.Fatal("expected owner-scoped read error")
	}
	return err
}
