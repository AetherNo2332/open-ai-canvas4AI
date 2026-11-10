package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"time"
	"unicode/utf8"
	"yingce/backend/internal/model"
)

// ErrDramaPlanPending 表示还有章节在等待分集规划，此时不能逐集追加。
var ErrDramaPlanPending = errors.New("drama plan pending")

func (r *Repository) DramaProduction(projectID string) (*model.DramaProduction, error) {
	var production model.DramaProduction
	if err := r.db.First(&production, "project_id = ?", projectID).Error; err != nil {
		return nil, err
	}
	return &production, nil
}

// RunningDramaProductions 返回需要调度循环推进的项目：运行中的项目，以及暂停或已完成但仍有已派发任务的项目。
// 后者只回收任务结果（已付费的视频、尾帧），不再派发新工作。
func (r *Repository) RunningDramaProductions(limit int) ([]model.DramaProduction, error) {
	var productions []model.DramaProduction
	pending := r.db.Model(&model.DramaJob{}).Select("project_id").Where("status = ?", model.DramaJobRunning)
	err := r.db.Where("status = ? OR project_id IN (?)", model.DramaStatusRunning, pending).Order("updated_at asc").Limit(limit).Find(&productions).Error
	return productions, err
}

// CreateDramaProject 在同一事务内写入项目、章节与制作控制面，避免出现无章节的半成品项目。
// episodes 非空表示每章直接成为一集（逐集制作），不经过分集规划。
func (r *Repository) CreateDramaProject(project *model.Project, chapters []model.ProjectUnit, production *model.DramaProduction, episodes ...model.DramaEpisode) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "name"}}, DoNothing: true}).Create(project)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrProjectNameConflict
		}
		if len(chapters) > 0 {
			if err := tx.CreateInBatches(&chapters, 200).Error; err != nil {
				return err
			}
		}
		if len(episodes) > 0 {
			if err := tx.CreateInBatches(&episodes, 200).Error; err != nil {
				return err
			}
		}
		return tx.Create(production).Error
	})
}

// ErrDramaDraftStarted 表示要重存的草稿已经开工（状态不是草稿或已有作业）。
var ErrDramaDraftStarted = errors.New("drama draft started")

// ReplaceDramaDraft 在同一事务内整体替换草稿：项目名称与画幅、原著章节、按章分集的集和制作控制面。
// 只在作品仍是草稿且没有任何作业时替换；名称与同用户其他项目重复时返回 ErrProjectNameConflict。
func (r *Repository) ReplaceDramaDraft(project *model.Project, chapters []model.ProjectUnit, production *model.DramaProduction, episodes []model.DramaEpisode) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current model.DramaProduction
		if err := tx.First(&current, "project_id = ? AND user_id = ?", project.ID, project.UserID).Error; err != nil {
			return err
		}
		var jobs int64
		if err := tx.Model(&model.DramaJob{}).Where("project_id = ?", project.ID).Count(&jobs).Error; err != nil {
			return err
		}
		if current.Status != model.DramaStatusDraft || jobs > 0 {
			return ErrDramaDraftStarted
		}
		var taken int64
		if err := tx.Model(&model.Project{}).Where("user_id = ? AND name = ? AND id <> ?", project.UserID, project.Name, project.ID).Count(&taken).Error; err != nil {
			return err
		}
		if taken > 0 {
			return ErrProjectNameConflict
		}
		if err := tx.Model(&model.Project{}).Where("id = ? AND user_id = ?", project.ID, project.UserID).Updates(map[string]any{
			"name": project.Name, "aspect_ratio": project.AspectRatio, "source_type": project.SourceType,
			"revision": gorm.Expr("revision + 1"), "updated_at": project.UpdatedAt,
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND kind = ?", project.ID, model.ProjectUnitKindChapter).Delete(&model.ProjectUnit{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ?", project.ID).Delete(&model.DramaEpisode{}).Error; err != nil {
			return err
		}
		if len(chapters) > 0 {
			if err := tx.CreateInBatches(&chapters, 200).Error; err != nil {
				return err
			}
		}
		if len(episodes) > 0 {
			if err := tx.CreateInBatches(&episodes, 200).Error; err != nil {
				return err
			}
		}
		production.Revision = current.Revision + 1
		return tx.Select("*").Where("project_id = ?", project.ID).Updates(production).Error
	})
}

// DramaEpisodePlacement 指定新的一集插在哪一集之前或之后。
type DramaEpisodePlacement struct {
	AnchorEpisodeID string
	Before          bool
}

// AddDramaEpisodeSource 写入一章原文和对应的一集，并把分集游标推过这一章，调度循环不会再把它交给分集规划。
// placement 为 nil 时追加到末尾；否则插到锚点集之前或之后：后续集号顺延，原文插到锚点集章节的前后，
// 之后的章节位置与造型的默认适用章节号一起后移。标题为空时按最终集号取默认标题。
// 只允许在分集规划完成后写入，否则游标会把新章节并入规划批次。
func (r *Repository) AddDramaEpisodeSource(projectID string, chapter *model.ProjectUnit, episode *model.DramaEpisode, placement *DramaEpisodePlacement) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var production model.DramaProduction
		if err := tx.First(&production, "project_id = ?", projectID).Error; err != nil {
			return err
		}
		if !production.PlanDone {
			return ErrDramaPlanPending
		}
		// 规划游标只数已加入制作的章节；追加时位置仍排在全部章节（含预留章节）之后。
		var count int64
		if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ? AND status <> ?", projectID, model.ProjectUnitKindChapter, model.ProjectUnitStatusReserved).Count(&count).Error; err != nil {
			return err
		}
		var position int
		if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ?", projectID, model.ProjectUnitKindChapter).Select("COALESCE(MAX(position), -1)").Scan(&position).Error; err != nil {
			return err
		}
		var number int
		if err := tx.Model(&model.DramaEpisode{}).Where("project_id = ?", projectID).Select("COALESCE(MAX(number), 0)").Scan(&number).Error; err != nil {
			return err
		}
		chapter.Position = position + 1
		episode.Number = number + 1
		if placement != nil {
			var anchor model.DramaEpisode
			if err := tx.Select("id", "number", "chapter_ids_json").First(&anchor, "id = ? AND project_id = ?", placement.AnchorEpisodeID, projectID).Error; err != nil {
				return err
			}
			episode.Number = anchor.Number + 1
			if placement.Before {
				episode.Number = anchor.Number
			}
			var positions []int
			if ids := decodeDramaIDs(anchor.ChapterIDsJSON); len(ids) > 0 {
				if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ? AND id IN ?", projectID, model.ProjectUnitKindChapter, ids).Pluck("position", &positions).Error; err != nil {
					return err
				}
			}
			// 锚点集没有关联章节时原文仍排到末尾，只调整集号。
			if len(positions) > 0 {
				chapter.Position = slices.Max(positions) + 1
				if placement.Before {
					chapter.Position = slices.Min(positions)
				}
				if err := shiftDramaChaptersForInsert(tx, projectID, chapter.Position); err != nil {
					return err
				}
			}
			if err := shiftDramaEpisodeNumbers(tx, projectID, episode.Number, 1); err != nil {
				return err
			}
		}
		if chapter.Title == "" {
			chapter.Title = model.DramaEpisodeDefaultTitle(episode.Number)
		}
		if episode.Title == "" {
			episode.Title = model.DramaEpisodeDefaultTitle(episode.Number)
		}
		if err := tx.Create(chapter).Error; err != nil {
			return err
		}
		if err := tx.Create(episode).Error; err != nil {
			return err
		}
		return tx.Model(&model.DramaProduction{}).Where("project_id = ?", projectID).
			Updates(map[string]any{"plan_cursor": count + 1, "plan_done": true, "updated_at": time.Now()}).Error
	})
}

var (
	// ErrDramaLastEpisode 表示作品只剩这一集，不能再删。
	ErrDramaLastEpisode = errors.New("drama last episode")
	// ErrDramaEpisodeBusy 表示这一集还有已提交的生成任务在进行，结果回写前不能删除。
	ErrDramaEpisodeBusy = errors.New("drama episode busy")
	// ErrDramaEpisodeConfirmRequired 表示这一集生成过视频，删除需要用户输入确认。
	ErrDramaEpisodeConfirmRequired = errors.New("drama episode confirm required")
)

// DeleteDramaEpisode 删除一集及其镜头、剧情段、段视频与成片的版本记录和调度作业，后续集号前移一位。
// releaseChapterIDs 是只被这一集使用的章节，退回预留状态（原文保留，可在原著章节里重新加入制作），规划游标随之重算。
// 这一集生成过视频且 confirmed=false 时返回 ErrDramaEpisodeConfirmRequired，不做任何修改。
func (r *Repository) DeleteDramaEpisode(projectID, episodeID string, releaseChapterIDs []string, confirmed bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var production model.DramaProduction
		if err := tx.First(&production, "project_id = ?", projectID).Error; err != nil {
			return err
		}
		if !production.PlanDone {
			return ErrDramaPlanPending
		}
		var episode model.DramaEpisode
		if err := tx.Select("id", "number", "final_resource_id").First(&episode, "id = ? AND project_id = ?", episodeID, projectID).Error; err != nil {
			return err
		}
		var total int64
		if err := tx.Model(&model.DramaEpisode{}).Where("project_id = ?", projectID).Count(&total).Error; err != nil {
			return err
		}
		if total <= 1 {
			return ErrDramaLastEpisode
		}
		// 已提交的任务已经计费，结果要回写到本集的剧情段；等它们结束再删，避免付费结果无处落地。
		var running int64
		if err := tx.Model(&model.DramaJob{}).Where("project_id = ? AND episode_id = ? AND status = ?", projectID, episode.ID, model.DramaJobRunning).Count(&running).Error; err != nil {
			return err
		}
		if running > 0 {
			return ErrDramaEpisodeBusy
		}
		if !confirmed {
			generated, err := dramaEpisodeHasVideo(tx, projectID, episode)
			if err != nil {
				return err
			}
			if generated {
				return ErrDramaEpisodeConfirmRequired
			}
		}
		segments := tx.Model(&model.DramaSegment{}).Select("id").Where("episode_id = ?", episode.ID)
		if err := tx.Where("project_id = ? AND ((kind = ? AND owner_id IN (?)) OR (kind = ? AND owner_id = ?))", projectID, "video", segments, "final", episode.ID).Delete(&model.DramaTake{}).Error; err != nil {
			return err
		}
		if err := tx.Where("project_id = ? AND episode_id = ?", projectID, episode.ID).Delete(&model.DramaJob{}).Error; err != nil {
			return err
		}
		for _, entity := range []any{&model.DramaSegment{}, &model.DramaShot{}} {
			if err := tx.Where("project_id = ? AND episode_id = ?", projectID, episode.ID).Delete(entity).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("id = ? AND project_id = ?", episode.ID, projectID).Delete(&model.DramaEpisode{}).Error; err != nil {
			return err
		}
		if err := shiftDramaEpisodeNumbers(tx, projectID, episode.Number+1, -1); err != nil {
			return err
		}
		now := time.Now()
		if len(releaseChapterIDs) > 0 {
			if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ? AND id IN ?", projectID, model.ProjectUnitKindChapter, releaseChapterIDs).
				Updates(map[string]any{"status": model.ProjectUnitStatusReserved, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		var count int64
		if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ? AND status <> ?", projectID, model.ProjectUnitKindChapter, model.ProjectUnitStatusReserved).Count(&count).Error; err != nil {
			return err
		}
		return tx.Model(&model.DramaProduction{}).Where("project_id = ?", projectID).Updates(map[string]any{"plan_cursor": count, "updated_at": now}).Error
	})
}

// dramaEpisodeHasVideo 判断一集是否生成过视频：成片、当前段视频、段视频与成片的历史版本，
// 以及成功的段视频或合成作业（重新改编会删掉旧剧情段，旧段视频只能从作业记录对上本集）。
func dramaEpisodeHasVideo(tx *gorm.DB, projectID string, episode model.DramaEpisode) (bool, error) {
	if episode.FinalResourceID != "" {
		return true, nil
	}
	segments := tx.Model(&model.DramaSegment{}).Select("id").Where("episode_id = ?", episode.ID)
	checks := []*gorm.DB{
		tx.Model(&model.DramaSegment{}).Where("project_id = ? AND episode_id = ? AND video_resource_id <> ''", projectID, episode.ID),
		tx.Model(&model.DramaTake{}).Where("project_id = ? AND ((kind = ? AND owner_id IN (?)) OR (kind = ? AND owner_id = ?))", projectID, "video", segments, "final", episode.ID),
		tx.Model(&model.DramaJob{}).Where("project_id = ? AND episode_id = ? AND kind IN ? AND status = ?", projectID, episode.ID, []string{model.DramaJobSegmentVideo, model.DramaJobRender}, model.DramaJobSucceeded),
	}
	for _, check := range checks {
		var count int64
		if err := check.Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

// shiftDramaChaptersForInsert 为插到 position 的新章节腾位置：之后的章节（含预留章节）后移一位，
// 造型的默认适用章节号（从 1 起，0 表示不限）同步后移，仍对应原来那些章节。
func shiftDramaChaptersForInsert(tx *gorm.DB, projectID string, position int) error {
	if err := tx.Model(&model.ProjectUnit{}).Where("project_id = ? AND kind = ? AND position >= ?", projectID, model.ProjectUnitKindChapter, position).
		Update("position", gorm.Expr("position + 1")).Error; err != nil {
		return err
	}
	for _, column := range []string{"look_from_chapter", "look_to_chapter"} {
		if err := tx.Model(&model.DramaCast{}).Where("project_id = ? AND "+column+" >= ?", projectID, position+1).
			Update(column, gorm.Expr(column+" + 1")).Error; err != nil {
			return err
		}
	}
	return nil
}

// shiftDramaEpisodeNumbers 把 from 号及之后各集的集号加 delta（插入 +1，删除 -1）。(project_id, number) 唯一，
// 逐行加减会和相邻集号冲突，所以先翻成负数再写回。仍是默认标题的集随新集号改名，以“第 N 集”开头的作业标签同步改号。
func shiftDramaEpisodeNumbers(tx *gorm.DB, projectID string, from, delta int) error {
	var episodes []model.DramaEpisode
	if err := tx.Select("id", "number", "title").Where("project_id = ? AND number >= ?", projectID, from).Find(&episodes).Error; err != nil {
		return err
	}
	if len(episodes) == 0 {
		return nil
	}
	if err := tx.Model(&model.DramaEpisode{}).Where("project_id = ? AND number >= ?", projectID, from).
		Update("number", gorm.Expr(fmt.Sprintf("-(number + %d)", delta))).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.DramaEpisode{}).Where("project_id = ? AND number < 0", projectID).Update("number", gorm.Expr("-number")).Error; err != nil {
		return err
	}
	for _, episode := range episodes {
		before, after := model.DramaEpisodeDefaultTitle(episode.Number), model.DramaEpisodeDefaultTitle(episode.Number+delta)
		if episode.Title == before {
			if err := tx.Model(&model.DramaEpisode{}).Where("id = ?", episode.ID).Update("title", after).Error; err != nil {
				return err
			}
		}
		// “第 3 集%”不会匹配“第 30 集”：集号后面紧跟空格。
		// 只改标签不动 updated_at，队列历史仍按原来的时间排序。
		if err := tx.Model(&model.DramaJob{}).Where("project_id = ? AND episode_id = ? AND label LIKE ?", projectID, episode.ID, before+"%").
			UpdateColumn("label", gorm.Expr(fmt.Sprintf("CAST(? AS TEXT) || SUBSTR(label, %d)", utf8.RuneCountInString(before)+1), after)).Error; err != nil {
			return err
		}
	}
	return nil
}

func decodeDramaIDs(raw string) []string {
	var ids []string
	_ = json.Unmarshal([]byte(raw), &ids)
	return ids
}

// UpdateDramaChapterSource 修改一章原文与字数。
func (r *Repository) UpdateDramaChapterSource(projectID, chapterID string, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.Model(&model.ProjectUnit{}).Where("id = ? AND project_id = ? AND kind = ?", chapterID, projectID, model.ProjectUnitKindChapter).Updates(fields).Error
}

// InsertDramaShot 在同一剧情段内插入镜头：后续镜头顺延，本段提示词与视频过期。
func (r *Repository) InsertDramaShot(shot model.DramaShot) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.DramaShot{}).Where("episode_id = ? AND position >= ?", shot.EpisodeID, shot.Position).
			Update("position", gorm.Expr("position + 1")).Error; err != nil {
			return err
		}
		if err := tx.Create(&shot).Error; err != nil {
			return err
		}
		return refreshDramaSegmentAfterShotChange(tx, shot)
	})
}

// DeleteDramaShot 删除镜头并前移后续镜头；调用方保证本段还剩其他镜头。
func (r *Repository) DeleteDramaShot(shot model.DramaShot) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND project_id = ?", shot.ID, shot.ProjectID).Delete(&model.DramaShot{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.DramaShot{}).Where("episode_id = ? AND position > ?", shot.EpisodeID, shot.Position).
			Update("position", gorm.Expr("position - 1")).Error; err != nil {
			return err
		}
		return refreshDramaSegmentAfterShotChange(tx, shot)
	})
}

func refreshDramaSegmentAfterShotChange(tx *gorm.DB, shot model.DramaShot) error {
	var segment model.DramaSegment
	if err := tx.Where("episode_id = ? AND segment_index = ?", shot.EpisodeID, shot.SegmentIndex).First(&segment).Error; err != nil {
		return err
	}
	if err := invalidateDramaSegment(tx, segment, map[string]any{"prompt_status": promptStatusAfterInputChange(segment.PromptStatus), "video_prompt_override": ""}, true); err != nil {
		return err
	}
	if err := tx.Model(&model.DramaEpisode{}).Where("id = ?", shot.EpisodeID).Update("duration_ms", tx.Model(&model.DramaShot{}).Select("COALESCE(SUM(duration_ms),0)").Where("episode_id = ?", shot.EpisodeID)).Error; err != nil {
		return err
	}
	return tx.Model(&model.DramaEpisode{}).Where("id = ?", shot.EpisodeID).Update("shot_count", tx.Model(&model.DramaShot{}).Select("COUNT(*)").Where("episode_id = ?", shot.EpisodeID)).Error
}
