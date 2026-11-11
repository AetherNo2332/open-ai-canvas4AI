package poselibrary

import (
	"time"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func buildImportRecords(sourceKey, actorID string, candidates []importCandidate, existing map[string]model.PoseLibraryItem, sortBase int, now time.Time) ([]model.PoseLibraryItem, []model.PoseLibraryItem) {
	created := make([]model.PoseLibraryItem, 0, len(candidates))
	updated := make([]model.PoseLibraryItem, 0)
	for _, item := range candidates {
		entry := item.entry
		record, found := existing[entry.key]
		if !found {
			record = model.PoseLibraryItem{ID: kernel.NewID(), SourceKey: sourceKey, ExternalKey: entry.key, Enabled: true, Status: model.PoseLibraryStatusPending, CreatedBy: actorID, CreatedAt: now}
		}
		record.GroupKey = sourceKey + ":" + entry.group
		record.Code = entry.code
		record.System = entry.system
		record.Category = entry.category
		record.Name = entry.name
		record.Angle = entry.angle
		record.SearchText = searchText(append([]string{entry.name, entry.category, entry.system, entry.code, entry.angle, entry.key}, entry.keywords...)...)
		record.SortOrder = sortBase + item.order
		record.RemoteThumb = item.remoteThumb
		record.RemoteImage = item.remoteImage
		record.UpdatedAt = now
		if record.Status == model.PoseLibraryStatusFailed {
			// 换了来源地址或重新导入后让失败条目重新排队。
			record.Status = model.PoseLibraryStatusPending
			record.Error = ""
		}
		if found {
			updated = append(updated, record)
		} else {
			created = append(created, record)
		}
	}
	return created, updated
}
