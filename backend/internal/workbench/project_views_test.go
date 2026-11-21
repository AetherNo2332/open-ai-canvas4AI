package workbench

import (
	"errors"
	"reflect"
	"testing"

	"yingce/backend/internal/model"
)

func TestNormalizePageAndPageState(t *testing.T) {
	tests := []struct {
		name         string
		page         int
		pageSize     int
		maximum      int
		total        int64
		wantPage     int
		wantPageSize int
		wantHasMore  bool
	}{
		{name: "empty defaults", page: 0, pageSize: -1, maximum: 100, total: 0, wantPage: 1, wantPageSize: 40, wantHasMore: false},
		{name: "page size cap", page: 1, pageSize: 101, maximum: 100, total: 100, wantPage: 1, wantPageSize: 100, wantHasMore: false},
		{name: "first partial page", page: 1, pageSize: 40, maximum: 100, total: 41, wantPage: 1, wantPageSize: 40, wantHasMore: true},
		{name: "exact last page", page: 4, pageSize: 25, maximum: 100, total: 100, wantPage: 4, wantPageSize: 25, wantHasMore: false},
		{name: "partial last page", page: 3, pageSize: 25, maximum: 100, total: 51, wantPage: 3, wantPageSize: 25, wantHasMore: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			page, pageSize := NormalizePage(testCase.page, testCase.pageSize, testCase.maximum)
			state := NewPageState(page, pageSize, testCase.total)
			want := PageState{Page: testCase.wantPage, PageSize: testCase.wantPageSize, Total: testCase.total, HasMore: testCase.wantHasMore}
			if state != want {
				t.Fatalf("page state = %+v, want %+v", state, want)
			}
		})
	}
}

func TestBuildOverviewMetricsMapsTimelineRenderCount(t *testing.T) {
	row := OverviewMetricRow{
		UnitCount: 1, CompletedUnitCount: 2, TotalWordCount: 3, UnitsWithoutText: 4, UnitsWithoutShots: 5,
		CanvasCount: 6, AssetCount: 7, ShotCount: 8, PendingCandidateCount: 9, ReadyStoryboardCount: 10,
		ReadyPrevizCount: 11, ReadyVideoCount: 12, TimelineRenderSucceededCount: 13, StaleArtifactCount: 14,
	}
	want := OverviewMetrics{
		UnitCount: 1, CompletedUnitCount: 2, TotalWordCount: 3, UnitsWithoutText: 4, UnitsWithoutShots: 5,
		CanvasCount: 6, AssetCount: 7, ShotCount: 8, PendingCandidateCount: 9, ReadyStoryboardCount: 10,
		ReadyPrevizCount: 11, ReadyVideoCount: 12, RenderSucceededCount: 13, StaleArtifactCount: 14,
	}
	if got := BuildOverviewMetrics(row); got != want {
		t.Fatalf("overview metrics = %+v, want %+v", got, want)
	}
}

func TestBuildOverviewUnitsPreservesInputOrderAndEmptySlices(t *testing.T) {
	if units := BuildOverviewUnits(nil); units == nil || len(units) != 0 {
		t.Fatalf("nil overview rows = %#v, want non-nil empty slice", units)
	}
	rows := []OverviewUnitRow{
		{Unit: model.ProjectUnit{ID: "unit-z", Title: "后"}, ShotCount: 2, CandidateCount: 3, CanvasCount: 4},
		{Unit: model.ProjectUnit{ID: "unit-a", Title: "前"}, ShotCount: 5, CandidateCount: 6, CanvasCount: 7},
	}
	want := []OverviewUnit{
		{Unit: model.ProjectUnit{ID: "unit-z", Title: "后"}, ShotCount: 2, CandidateCount: 3, CanvasCount: 4},
		{Unit: model.ProjectUnit{ID: "unit-a", Title: "前"}, ShotCount: 5, CandidateCount: 6, CanvasCount: 7},
	}
	if got := BuildOverviewUnits(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("overview units = %#v, want %#v", got, want)
	}
}

func TestBuildAssetFacetCounts(t *testing.T) {
	categories, folders := BuildAssetFacetCounts(nil, nil)
	if categories == nil || folders == nil || len(categories) != 0 || len(folders) != 0 {
		t.Fatalf("empty facets = %#v / %#v, want non-nil empty maps", categories, folders)
	}
	categories, folders = BuildAssetFacetCounts(
		[]AssetCountRow{{Key: "prop", Count: 2}, {Key: "character", Count: 1}},
		[]AssetCountRow{{Key: "folder-b", Count: 4}, {Key: "folder-a", Count: 2}},
	)
	wantCategories := map[string]int64{"prop": 2, "character": 1}
	wantFolders := map[string]int64{"folder-b": 4, "folder-a": 2}
	if !reflect.DeepEqual(categories, wantCategories) || !reflect.DeepEqual(folders, wantFolders) {
		t.Fatalf("facets = %#v / %#v, want %#v / %#v", categories, folders, wantCategories, wantFolders)
	}
}

func TestAssembleShotReferencesPreservesInputOrderAndEmptyRepresentations(t *testing.T) {
	references := []model.ShotAssetReference{
		{ID: "reference-b", ShotID: "shot-1", AssetVersionID: "version-2", Role: "reference", Status: "linked"},
		{ID: "reference-a", ShotID: "shot-1", AssetVersionID: "version-1", Role: "reference", Status: "linked"},
	}
	versions := []model.AssetVersion{
		{ID: "version-1", AssetID: "asset-1", Version: 1},
		{ID: "version-2", AssetID: "asset-2", Version: 2},
	}
	representations := []model.AssetRepresentation{
		{ID: "representation-b", AssetVersionID: "version-1", ResourceID: "resource-b", MediaType: "image/png", Role: "detail"},
		{ID: "representation-a", AssetVersionID: "version-1", ResourceID: "resource-a", MediaType: "image/png", Role: "primary"},
	}
	assets := map[string]string{"asset-1": "current-1", "asset-2": "current-2"}
	got, err := AssembleShotReferences(references, versions, representations, assets)
	if err != nil {
		t.Fatal(err)
	}
	want := []ShotReference[string]{
		{
			Reference: references[0], Asset: "current-2",
			ReferencedVersion: ShotReferenceVersion{ID: "version-2", AssetID: "asset-2", Version: 2, Representations: []ShotReferenceRepresentation{}},
		},
		{
			Reference: references[1], Asset: "current-1",
			ReferencedVersion: ShotReferenceVersion{
				ID: "version-1", AssetID: "asset-1", Version: 1,
				Representations: []ShotReferenceRepresentation{
					{ID: "representation-b", ResourceID: "resource-b", MediaType: "image/png", Role: "detail"},
					{ID: "representation-a", ResourceID: "resource-a", MediaType: "image/png", Role: "primary"},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shot references = %#v, want %#v", got, want)
	}

	empty, err := AssembleShotReferences[string](nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty references = %#v, want non-nil empty slice", empty)
	}
}

func TestAssembleShotReferencesRejectsMissingReferences(t *testing.T) {
	references := []model.ShotAssetReference{{ID: "reference-1", AssetVersionID: "version-1"}}
	tests := []struct {
		name     string
		versions []model.AssetVersion
		assets   map[string]string
		wantErr  error
	}{
		{
			name: "missing version", assets: map[string]string{"asset-1": "current"},
			wantErr: ErrShotAssetVersionUnavailable,
		},
		{
			name: "missing project asset", versions: []model.AssetVersion{{ID: "version-1", AssetID: "asset-1", Version: 1}},
			wantErr: ErrShotProjectAssetUnavailable,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := AssembleShotReferences(references, testCase.versions, nil, testCase.assets)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
			if got != nil {
				t.Fatalf("references = %#v, want nil on error", got)
			}
		})
	}
}

func TestFilterTasksByUnitPreservesInputOrder(t *testing.T) {
	type task struct {
		ID      string
		Context *UnitTaskContext
	}
	tasks := []task{
		{ID: "task-chapter", Context: &UnitTaskContext{ChapterID: "unit-1"}},
		{ID: "task-unrelated", Context: &UnitTaskContext{ShotID: "shot-other"}},
		{ID: "task-shot", Context: &UnitTaskContext{ShotID: "shot-1"}},
		{ID: "task-no-context"},
		{ID: "task-empty-context", Context: &UnitTaskContext{}},
	}
	got := FilterTasksByUnit(tasks, "unit-1", []string{"shot-1"}, func(item task) (UnitTaskContext, bool) {
		if item.Context == nil {
			return UnitTaskContext{}, false
		}
		return *item.Context, true
	})
	want := []task{tasks[0], tasks[2]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered tasks = %#v, want %#v", got, want)
	}
	empty := FilterTasksByUnit[task](nil, "unit-1", nil, func(item task) (UnitTaskContext, bool) {
		return UnitTaskContext{}, false
	})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty filtered tasks = %#v, want non-nil empty slice", empty)
	}
}

func TestShotReferenceIDsPreserveInputOrder(t *testing.T) {
	references := []model.ShotAssetReference{{AssetVersionID: "version-z"}, {AssetVersionID: "version-a"}}
	if got, want := ShotReferenceVersionIDs(references), []string{"version-z", "version-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("version ids = %#v, want %#v", got, want)
	}
	shots := []model.Shot{{ID: "shot-z"}, {ID: "shot-a"}}
	if got, want := ShotIDs(shots), []string{"shot-z", "shot-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shot ids = %#v, want %#v", got, want)
	}
	if ids := ShotReferenceVersionIDs(nil); ids == nil || len(ids) != 0 {
		t.Fatalf("empty version ids = %#v, want non-nil empty slice", ids)
	}
	if ids := ShotIDs(nil); ids == nil || len(ids) != 0 {
		t.Fatalf("empty shot ids = %#v, want non-nil empty slice", ids)
	}
}
