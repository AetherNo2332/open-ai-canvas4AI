package app

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func TestProjectWorkbenchReadEntrypointsRequireProjectOwnership(t *testing.T) {
	service, db := newProjectWorkbenchReadTestService(t)
	project := seedWorkbenchProject(t, db)
	unit := model.ProjectUnit{ID: "unit-access", ProjectID: project.ID, Title: "授权测试", Status: model.ProjectUnitStatusReady}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}

	entrypoints := []struct {
		name string
		call func(userID string, projectID string) error
	}{
		{"core", func(userID string, projectID string) error {
			_, err := service.ProjectCore(userID, projectID)
			return err
		}},
		{"unit summaries", func(userID string, projectID string) error {
			_, err := service.ProjectUnitSummaries(userID, projectID)
			return err
		}},
		{"overview", func(userID string, projectID string) error {
			_, err := service.ProjectOverview(userID, projectID)
			return err
		}},
		{"unit workspace", func(userID string, projectID string) error {
			_, err := service.ProjectUnitWorkspace(userID, projectID, unit.ID)
			return err
		}},
		{"canvas page", func(userID string, projectID string) error {
			_, err := service.ProjectCanvasesPage(userID, projectID, 1, 20)
			return err
		}},
	}
	targets := []struct {
		name      string
		userID    string
		projectID string
	}{
		{"other user", "user-2", project.ID},
		{"missing project", "user-1", "missing-project"},
	}
	for _, entrypoint := range entrypoints {
		for _, target := range targets {
			t.Run(entrypoint.name+"/"+target.name, func(t *testing.T) {
				err := entrypoint.call(target.userID, target.projectID)
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("error = %v, want gorm.ErrRecordNotFound", err)
				}
			})
		}
	}
}

func TestProjectWorkbenchReadEmptyProjectKeepsEmptyCollections(t *testing.T) {
	service, db := newProjectWorkbenchReadTestService(t)
	project := model.Project{ID: "empty-project", UserID: "user-1", Name: "空项目", Status: model.ProjectStatusActive}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}

	core, err := service.ProjectCore("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if core.Project.ID != project.ID {
		t.Fatalf("project id = %q, want %q", core.Project.ID, project.ID)
	}

	summaries, err := service.ProjectUnitSummaries("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summaries.Units == nil || len(summaries.Units) != 0 {
		t.Fatalf("units = %#v, want non-nil empty slice", summaries.Units)
	}
	if summaries.CanvasCounts == nil || len(summaries.CanvasCounts) != 0 {
		t.Fatalf("canvas counts = %#v, want non-nil empty map", summaries.CanvasCounts)
	}

	overview, err := service.ProjectOverview("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Units == nil || len(overview.Units) != 0 {
		t.Fatalf("overview units = %#v, want non-nil empty slice", overview.Units)
	}
	if overview.Metrics != (ProjectOverviewMetrics{}) {
		t.Fatalf("overview metrics = %+v, want zero values", overview.Metrics)
	}

	unit := model.ProjectUnit{ID: "empty-unit", ProjectID: project.ID, Title: "空单元", Status: model.ProjectUnitStatusReady}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	workspace, err := service.ProjectUnitWorkspace("user-1", project.ID, unit.ID)
	if err != nil {
		t.Fatal(err)
	}
	collections := []struct {
		name  string
		isNil bool
	}{
		{"workflows", workspace.Workflows == nil},
		{"shots", workspace.Shots == nil},
		{"shot artifacts", workspace.ShotArtifacts == nil},
		{"shot references", workspace.ShotReferences == nil},
		{"asset candidates", workspace.AssetCandidates == nil},
		{"assets", workspace.Assets == nil},
		{"tasks", workspace.Tasks == nil},
	}
	for _, collection := range collections {
		if collection.isNil {
			t.Errorf("%s is nil, want non-nil empty collection", collection.name)
		}
	}
	if workspace.ShotRevisions != nil {
		t.Errorf("shot revisions = %#v, want baseline nil collection", workspace.ShotRevisions)
	}

	canvases, err := service.ProjectCanvasesPage("user-1", project.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if canvases.Canvases == nil || canvases.CanvasUnitLinks == nil {
		t.Fatalf("canvas page contains nil collections: %#v", canvases)
	}
	if canvases.Page != 1 || canvases.PageSize != 40 || canvases.Total != 0 || canvases.HasMore {
		t.Fatalf("canvas page = %+v, want normalized empty first page", canvases)
	}

	candidates, err := service.ProjectAssetCandidatesPage("user-1", project.ID, 0, 10_000, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if candidates.Candidates == nil {
		t.Fatal("asset candidates are nil, want non-nil empty slice")
	}
	if candidates.Page != 1 || candidates.PageSize != 200 || candidates.Total != 0 || candidates.HasMore {
		t.Fatalf("candidate page = %+v, want normalized empty first page", candidates)
	}

	assets, err := service.ProjectAssetsPage("user-1", project.ID, 0, 10_000, "", "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if assets.Assets == nil || assets.CategoryCounts == nil || assets.FolderCounts == nil {
		t.Fatalf("asset page contains nil collections: %#v", assets)
	}
	if assets.Page != 1 || assets.PageSize != 80 || assets.Total != 0 || assets.HasMore {
		t.Fatalf("asset page = %+v, want normalized empty first page", assets)
	}
}

func TestProjectWorkbenchReadPagesKeepStableOrderAndTotals(t *testing.T) {
	service, db := newProjectWorkbenchReadTestService(t)
	project := seedWorkbenchProject(t, db)
	base := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

	for index := 0; index < 5; index++ {
		canvasID := fmt.Sprintf("canvas-stable-%d", index)
		createdAt := base.Add(time.Duration(index) * time.Minute)
		canvas := model.CanvasProject{ID: canvasID, UserID: "user-1", ProjectID: project.ID, Title: canvasID, CreatedAt: createdAt, UpdatedAt: createdAt}
		link := model.CanvasUnitLink{ID: "canvas-link-stable-" + fmt.Sprint(index), ProjectID: project.ID, CanvasID: canvasID, CreatedAt: createdAt}
		for _, item := range []any{&canvas, &link} {
			if err := db.Create(item).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	canvasFirst, err := service.ProjectCanvasesPage("user-1", project.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertProjectWorkbenchStrings(t, "canvas first page", projectWorkbenchCanvasIDs(canvasFirst.Canvases), []string{"canvas-stable-4", "canvas-stable-3"})
	if canvasFirst.Total != 5 || !canvasFirst.HasMore || canvasFirst.Page != 1 || canvasFirst.PageSize != 2 {
		t.Fatalf("canvas first page metadata = %+v", canvasFirst)
	}
	canvasLast, err := service.ProjectCanvasesPage("user-1", project.ID, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertProjectWorkbenchStrings(t, "canvas last page", projectWorkbenchCanvasIDs(canvasLast.Canvases), []string{"canvas-stable-0"})
	if canvasLast.Total != 5 || canvasLast.HasMore || canvasLast.Page != 3 || canvasLast.PageSize != 2 {
		t.Fatalf("canvas last page metadata = %+v", canvasLast)
	}

	for index := 0; index < 5; index++ {
		updatedAt := base.Add(time.Duration(index) * time.Minute)
		asset := model.Asset{
			ID: fmt.Sprintf("asset-stable-%d", index), UserID: "user-1", Title: fmt.Sprintf("资产%d", index),
			Kind: "image", Category: model.AssetCategoryProp, Status: model.AssetVersionStatusConfirmed,
			PayloadJSON: `{}`, CreatedAt: updatedAt, UpdatedAt: updatedAt,
		}
		link := model.ProjectAssetLink{ID: "asset-link-stable-" + fmt.Sprint(index), ProjectID: project.ID, AssetID: asset.ID, FolderID: "folder-stable"}
		for _, item := range []any{&asset, &link} {
			if err := db.Create(item).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	assetFirst, err := service.ProjectAssetsPage("user-1", project.ID, 1, 2, "", "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertProjectWorkbenchStrings(t, "asset first page", projectWorkbenchAssetIDs(assetFirst.Assets), []string{"asset-stable-4", "asset-stable-3"})
	if assetFirst.Total != 5 || !assetFirst.HasMore {
		t.Fatalf("asset first page metadata = %+v", assetFirst)
	}
	assetLast, err := service.ProjectAssetsPage("user-1", project.ID, 3, 2, "", "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	assertProjectWorkbenchStrings(t, "asset last page", projectWorkbenchAssetIDs(assetLast.Assets), []string{"asset-stable-0"})
	if assetLast.Total != 5 || assetLast.HasMore {
		t.Fatalf("asset last page metadata = %+v", assetLast)
	}
	if assetLast.CategoryCounts["prop"] != 5 || assetLast.FolderCounts["folder-stable"] != 5 {
		t.Fatalf("asset facets = %#v / %#v", assetLast.CategoryCounts, assetLast.FolderCounts)
	}

	for index := 0; index < 5; index++ {
		createdAt := base.Add(time.Duration(index) * time.Minute)
		candidate := model.ProjectAssetCandidate{
			ID: fmt.Sprintf("candidate-stable-%d", index), ProjectID: project.ID, Name: fmt.Sprintf("候选%d", index),
			Category: model.AssetCategoryProp, Status: "pending_confirmation", CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		if err := db.Create(&candidate).Error; err != nil {
			t.Fatal(err)
		}
	}
	candidateFirst, err := service.ProjectAssetCandidatesPage("user-1", project.ID, 1, 2, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	assertProjectWorkbenchStrings(t, "candidate first page", projectWorkbenchCandidateIDs(candidateFirst.Candidates), []string{"candidate-stable-4", "candidate-stable-3"})
	if candidateFirst.Total != 5 || !candidateFirst.HasMore {
		t.Fatalf("candidate first page metadata = %+v", candidateFirst)
	}
	candidateLast, err := service.ProjectAssetCandidatesPage("user-1", project.ID, 3, 2, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	assertProjectWorkbenchStrings(t, "candidate last page", projectWorkbenchCandidateIDs(candidateLast.Candidates), []string{"candidate-stable-0"})
	if candidateLast.Total != 5 || candidateLast.HasMore {
		t.Fatalf("candidate last page metadata = %+v", candidateLast)
	}
}

func TestProjectUnitWorkspaceFiltersTasksByChapterAndShot(t *testing.T) {
	service, db := newProjectWorkbenchReadTestService(t)
	if err := db.AutoMigrate(&model.BillingOrder{}, &model.ApiCallLog{}); err != nil {
		t.Fatal(err)
	}
	project := seedWorkbenchProject(t, db)
	unit1 := model.ProjectUnit{ID: "task-unit-1", ProjectID: project.ID, Title: "第一章", Status: model.ProjectUnitStatusReady}
	unit2 := model.ProjectUnit{ID: "task-unit-2", ProjectID: project.ID, Title: "第二章", Position: 1, Status: model.ProjectUnitStatusReady}
	shot1 := model.Shot{ID: "task-shot-1", ProjectID: project.ID, UnitID: unit1.ID, Position: 0}
	shot2 := model.Shot{ID: "task-shot-2", ProjectID: project.ID, UnitID: unit2.ID, Position: 0}
	for _, item := range []any{&unit1, &unit2, &shot1, &shot2} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	tasks := []model.Task{
		{ID: "task-chapter", UserID: "user-1", ProjectID: project.ID, Type: "image_generation", Status: model.TaskStatusQueued, InputJSON: `{"metadata":{"source":"short-drama-chapter-storyboard","domainProjectId":"project-1","chapterId":"task-unit-1"}}`},
		{ID: "task-shot", UserID: "user-1", ProjectID: project.ID, Type: "image_generation", Status: model.TaskStatusQueued, InputJSON: `{"metadata":{"shotId":"task-shot-1","workflowStepId":"step-1"}}`},
		{ID: "task-other-unit", UserID: "user-1", ProjectID: project.ID, Type: "image_generation", Status: model.TaskStatusQueued, InputJSON: `{"metadata":{"shotId":"task-shot-2","workflowStepId":"step-1"}}`},
		{ID: "task-no-context", UserID: "user-1", ProjectID: project.ID, Type: "image_generation", Status: model.TaskStatusQueued, InputJSON: `{}`},
		{ID: "task-node-only", UserID: "user-1", ProjectID: project.ID, Type: "image_generation", Status: model.TaskStatusQueued, InputJSON: `{"metadata":{"nodeId":"node-1"}}`},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	workspace, err := service.ProjectUnitWorkspace("user-1", project.ID, unit1.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(workspace.Tasks))
	for _, task := range workspace.Tasks {
		got[task.ID] = true
	}
	for _, want := range []string{"task-chapter", "task-shot"} {
		if !got[want] {
			t.Fatalf("workspace tasks = %#v, missing %q", got, want)
		}
	}
	for _, unwanted := range []string{"task-other-unit", "task-no-context", "task-node-only"} {
		if got[unwanted] {
			t.Fatalf("workspace tasks = %#v, leaked %q", got, unwanted)
		}
	}
}

func TestProjectUnitWorkspaceMapsMissingShotReferencesToBadRequest(t *testing.T) {
	t.Run("missing version", func(t *testing.T) {
		service, db := newProjectWorkbenchReadTestService(t)
		project := seedWorkbenchProject(t, db)
		unit, shot := seedProjectWorkbenchUnitAndShot(t, db, project.ID, "missing-version")
		if err := db.Create(&model.ShotAssetReference{ID: "reference-missing-version", ShotID: shot.ID, AssetVersionID: "missing-version", Role: "reference", Status: "linked"}).Error; err != nil {
			t.Fatal(err)
		}
		_, err := service.ProjectUnitWorkspace("user-1", project.ID, unit.ID)
		requireProjectWorkbenchBadRequest(t, err, "镜头引用的资产版本不可用")
	})

	t.Run("missing project asset", func(t *testing.T) {
		service, db := newProjectWorkbenchReadTestService(t)
		project := seedWorkbenchProject(t, db)
		unit, shot := seedProjectWorkbenchUnitAndShot(t, db, project.ID, "missing-asset")
		asset := model.Asset{ID: "asset-other-user", UserID: "user-2", Title: "他人资产", Kind: "image", Category: model.AssetCategoryOther, Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{}`}
		version := model.AssetVersion{ID: "version-other-user", AssetID: asset.ID, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: `{}`}
		link := model.ProjectAssetLink{ID: "link-other-user", ProjectID: project.ID, AssetID: asset.ID}
		reference := model.ShotAssetReference{ID: "reference-other-user", ShotID: shot.ID, AssetVersionID: version.ID, Role: "reference", Status: "linked"}
		for _, item := range []any{&asset, &version, &link, &reference} {
			if err := db.Create(item).Error; err != nil {
				t.Fatal(err)
			}
		}
		_, err := service.ProjectUnitWorkspace("user-1", project.ID, unit.ID)
		requireProjectWorkbenchBadRequest(t, err, "镜头引用的项目资产不可用")
	})
}

func seedProjectWorkbenchUnitAndShot(t *testing.T, db *gorm.DB, projectID string, suffix string) (model.ProjectUnit, model.Shot) {
	t.Helper()
	unit := model.ProjectUnit{ID: "unit-" + suffix, ProjectID: projectID, Title: "镜头引用测试", Status: model.ProjectUnitStatusReady}
	shot := model.Shot{ID: "shot-" + suffix, ProjectID: projectID, UnitID: unit.ID, Position: 0}
	for _, item := range []any{&unit, &shot} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	return unit, shot
}

func requireProjectWorkbenchBadRequest(t *testing.T, err error, message string) {
	t.Helper()
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error = %v, want *AppError", err)
	}
	if appErr.Status != 400 || appErr.Message != message {
		t.Fatalf("error = %+v, want status 400 and message %q", appErr, message)
	}
}

func assertProjectWorkbenchStrings(t *testing.T, name string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func projectWorkbenchCanvasIDs(canvases []model.CanvasProject) []string {
	ids := make([]string, len(canvases))
	for index := range canvases {
		ids[index] = canvases[index].ID
	}
	return ids
}

func projectWorkbenchAssetIDs(assets []ProjectAssetSummary) []string {
	ids := make([]string, len(assets))
	for index := range assets {
		ids[index] = assets[index].ID
	}
	return ids
}

func projectWorkbenchCandidateIDs(candidates []model.ProjectAssetCandidate) []string {
	ids := make([]string, len(candidates))
	for index := range candidates {
		ids[index] = candidates[index].ID
	}
	return ids
}
