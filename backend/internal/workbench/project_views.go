package workbench

import (
	"errors"

	"yingce/backend/internal/model"
)

var (
	ErrShotAssetVersionUnavailable = errors.New("镜头引用的资产版本不可用")
	ErrShotProjectAssetUnavailable = errors.New("镜头引用的项目资产不可用")
)

type PageState struct {
	Page     int
	PageSize int
	Total    int64
	HasMore  bool
}

func NormalizePage(page int, pageSize int, maximum int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 40
	}
	if pageSize > maximum {
		pageSize = maximum
	}
	return page, pageSize
}

func NewPageState(page int, pageSize int, total int64) PageState {
	return PageState{
		Page: page, PageSize: pageSize, Total: total,
		HasMore: int64(page*pageSize) < total,
	}
}

type OverviewMetricRow struct {
	UnitCount                    int64
	CompletedUnitCount           int64
	TotalWordCount               int64
	UnitsWithoutText             int64
	UnitsWithoutShots            int64
	CanvasCount                  int64
	AssetCount                   int64
	ShotCount                    int64
	PendingCandidateCount        int64
	ReadyStoryboardCount         int64
	ReadyPrevizCount             int64
	ReadyVideoCount              int64
	TimelineRenderSucceededCount int64
	StaleArtifactCount           int64
}

type OverviewMetrics struct {
	UnitCount             int64
	CompletedUnitCount    int64
	TotalWordCount        int64
	UnitsWithoutText      int64
	UnitsWithoutShots     int64
	CanvasCount           int64
	AssetCount            int64
	ShotCount             int64
	PendingCandidateCount int64
	ReadyStoryboardCount  int64
	ReadyPrevizCount      int64
	ReadyVideoCount       int64
	RenderSucceededCount  int64
	StaleArtifactCount    int64
}

func BuildOverviewMetrics(row OverviewMetricRow) OverviewMetrics {
	return OverviewMetrics{
		UnitCount: row.UnitCount, CompletedUnitCount: row.CompletedUnitCount, TotalWordCount: row.TotalWordCount,
		UnitsWithoutText: row.UnitsWithoutText, UnitsWithoutShots: row.UnitsWithoutShots, CanvasCount: row.CanvasCount,
		AssetCount: row.AssetCount, ShotCount: row.ShotCount, PendingCandidateCount: row.PendingCandidateCount,
		ReadyStoryboardCount: row.ReadyStoryboardCount, ReadyPrevizCount: row.ReadyPrevizCount, ReadyVideoCount: row.ReadyVideoCount,
		RenderSucceededCount: row.TimelineRenderSucceededCount, StaleArtifactCount: row.StaleArtifactCount,
	}
}

type OverviewUnitRow struct {
	Unit           model.ProjectUnit
	ShotCount      int64
	CandidateCount int64
	CanvasCount    int64
}

type OverviewUnit struct {
	Unit           model.ProjectUnit
	ShotCount      int64
	CandidateCount int64
	CanvasCount    int64
}

func BuildOverviewUnits(rows []OverviewUnitRow) []OverviewUnit {
	units := make([]OverviewUnit, 0, len(rows))
	for _, row := range rows {
		units = append(units, OverviewUnit{Unit: row.Unit, ShotCount: row.ShotCount, CandidateCount: row.CandidateCount, CanvasCount: row.CanvasCount})
	}
	return units
}

type AssetCountRow struct {
	Key   string
	Count int64
}

func BuildAssetFacetCounts(categoryRows []AssetCountRow, folderRows []AssetCountRow) (map[string]int64, map[string]int64) {
	categoryCounts := make(map[string]int64, len(categoryRows))
	for _, row := range categoryRows {
		categoryCounts[row.Key] = row.Count
	}
	folderCounts := make(map[string]int64, len(folderRows))
	for _, row := range folderRows {
		folderCounts[row.Key] = row.Count
	}
	return categoryCounts, folderCounts
}

type ShotReferenceRepresentation struct {
	ID         string
	ResourceID string
	MediaType  string
	Role       string
}

type ShotReferenceVersion struct {
	ID              string
	AssetID         string
	Version         int
	Representations []ShotReferenceRepresentation
}

type ShotReference[T any] struct {
	Reference         model.ShotAssetReference
	Asset             T
	ReferencedVersion ShotReferenceVersion
}

func ShotReferenceVersionIDs(references []model.ShotAssetReference) []string {
	versionIDs := make([]string, 0, len(references))
	for _, reference := range references {
		versionIDs = append(versionIDs, reference.AssetVersionID)
	}
	return versionIDs
}

func AssembleShotReferences[T any](
	references []model.ShotAssetReference,
	versions []model.AssetVersion,
	representations []model.AssetRepresentation,
	assets map[string]T,
) ([]ShotReference[T], error) {
	versionByID := make(map[string]model.AssetVersion, len(versions))
	for _, version := range versions {
		versionByID[version.ID] = version
	}
	representationsByVersionID := make(map[string][]ShotReferenceRepresentation, len(versions))
	for _, representation := range representations {
		representationsByVersionID[representation.AssetVersionID] = append(representationsByVersionID[representation.AssetVersionID], ShotReferenceRepresentation{
			ID: representation.ID, ResourceID: representation.ResourceID, MediaType: representation.MediaType, Role: representation.Role,
		})
	}
	result := make([]ShotReference[T], 0, len(references))
	for _, reference := range references {
		version, exists := versionByID[reference.AssetVersionID]
		if !exists {
			return nil, ErrShotAssetVersionUnavailable
		}
		asset, exists := assets[version.AssetID]
		if !exists {
			return nil, ErrShotProjectAssetUnavailable
		}
		versionRepresentations := representationsByVersionID[version.ID]
		if versionRepresentations == nil {
			versionRepresentations = []ShotReferenceRepresentation{}
		}
		result = append(result, ShotReference[T]{
			Reference: reference,
			Asset:     asset,
			ReferencedVersion: ShotReferenceVersion{
				ID: version.ID, AssetID: version.AssetID, Version: version.Version, Representations: versionRepresentations,
			},
		})
	}
	return result, nil
}

func ShotIDs(shots []model.Shot) []string {
	shotIDs := make([]string, 0, len(shots))
	for _, shot := range shots {
		shotIDs = append(shotIDs, shot.ID)
	}
	return shotIDs
}

type UnitTaskContext struct {
	ChapterID string
	ShotID    string
}

func FilterTasksByUnit[T any](tasks []T, unitID string, shotIDs []string, contextOf func(T) (UnitTaskContext, bool)) []T {
	shotSet := make(map[string]struct{}, len(shotIDs))
	for _, shotID := range shotIDs {
		shotSet[shotID] = struct{}{}
	}
	result := make([]T, 0, len(tasks))
	for _, task := range tasks {
		context, exists := contextOf(task)
		if !exists {
			continue
		}
		if context.ChapterID == unitID {
			result = append(result, task)
			continue
		}
		if _, belongsToShot := shotSet[context.ShotID]; belongsToShot {
			result = append(result, task)
		}
	}
	return result
}
