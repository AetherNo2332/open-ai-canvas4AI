package app

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

// 角色多造型（妆造）：一个角色（DramaCast kind=character, ParentID="")可以有多套造型，
// 每套造型是一行 DramaCast（kind=character, ParentID=角色ID, LookName=造型名），只承载这套造型的外观与参考图，
// 名称、别名与声音沿用所属角色。写剧本时按本集章节选默认造型，镜头可按剧情另选；生成段视频时按本镜造型取参考图。

// dramaLooksForCharacter 返回某角色的全部造型行（不含角色本体），按位置排序。
func dramaLooksForCharacter(casts []model.DramaCast, characterID string) []model.DramaCast {
	looks := []model.DramaCast{}
	for _, cast := range casts {
		if cast.Kind == "character" && cast.ParentID == characterID {
			looks = append(looks, cast)
		}
	}
	return looks
}

// dramaBaseCharacterID 把造型行归到所属角色；角色本体返回自身 ID。
func dramaBaseCharacterID(cast model.DramaCast) string {
	if cast.ParentID != "" {
		return cast.ParentID
	}
	return cast.ID
}

// dramaLookRanges 把一个角色的造型行转成 production.LookRange，供按章节选默认造型。
func dramaLookRanges(looks []model.DramaCast) []production.LookRange {
	ranges := make([]production.LookRange, 0, len(looks))
	for _, look := range looks {
		ranges = append(ranges, production.LookRange{ID: look.ID, From: look.LookFromChapter, To: look.LookToChapter})
	}
	return ranges
}

// dramaResolveShotLook 为一个镜头里的出镜角色挑出实际使用的造型行 ID：
// 镜头明确指定了造型名（lookChoices[角色名]）时用它，否则用本集默认造型；都没有时用角色本体（返回空串）。
// chapters 是本集的章节序号（从 1 起），用于计算默认造型。
func dramaResolveShotLook(character model.DramaCast, looks []model.DramaCast, chosenLookName string, chapters []int) string {
	if name := strings.TrimSpace(chosenLookName); name != "" && production.IsBaseLookName(name) {
		return ""
	}
	if len(looks) == 0 {
		return ""
	}
	if name := strings.TrimSpace(chosenLookName); name != "" && !production.IsBaseLookName(name) {
		for _, look := range looks {
			if production.NormalizeName(look.LookName) == production.NormalizeName(name) {
				return look.ID
			}
		}
	}
	return production.DefaultLook(dramaLookRanges(looks), chapters)
}

// dramaLookAppearance 是一套造型写进提示词与参考图的外观：造型自己的外观，空时退回角色本体外观。
func dramaLookAppearance(character, look model.DramaCast) string {
	if look.ID == "" || look.ID == character.ID {
		return firstNonEmpty(character.Appearance, character.Description)
	}
	return firstNonEmpty(look.Appearance, character.Appearance, character.Description)
}

func (s *Service) requeueFailedDramaLooks(projectID, castID string) error {
	casts, err := s.repo.DramaCasts(projectID)
	if err != nil {
		return err
	}
	for _, look := range casts {
		if look.ParentID != castID || look.ImageStatus != model.DramaMediaFailed {
			continue
		}
		if _, err := s.repo.UpdateDramaCastIf(look.ID, []string{model.DramaMediaFailed}, map[string]any{"image_status": model.DramaMediaQueued}); err != nil {
			return err
		}
	}
	return nil
}

func (r *dramaRun) planCastImages() error {
	for i := range r.casts {
		cast := &r.casts[i]
		// "" 表示从未请求，只有放行后才自动生成；queued 是用户明确请求。
		requested := cast.ImageStatus == model.DramaMediaQueued || (cast.ImageStatus == "" && r.assetsReleased())
		if !requested || cast.Locked {
			continue
		}
		ready, err := r.castLookBaseReady(cast)
		if err != nil {
			return err
		}
		if ready {
			r.enqueue(model.DramaJobCastImage, "", "", cast.ID, castImagePriority(cast.Position), "参考图："+cast.Name, nil)
		}
	}
	return nil
}

// 造型以角色本体参考图为底图，本体未就绪时等待，失败或删除时记录失败。
func (r *dramaRun) castLookBaseReady(cast *model.DramaCast) (bool, error) {
	if cast.ParentID == "" {
		return true, nil
	}
	base, err := r.s.repo.DramaCast(r.project.ID, cast.ParentID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return false, r.failCastLook(cast, "角色本体已被删除，无法生成造型参考图")
	case err != nil:
		return false, err
	case base.ImageStatus == model.DramaMediaFailed:
		return false, r.failCastLook(cast, "角色本体参考图生成失败，请先重试本体")
	default:
		return base.ImageStatus == model.DramaMediaReady, nil
	}
}

// failCastLook 按造型版本号落失败，同一次失败只创建一条待处理作业。
func (r *dramaRun) failCastLook(cast *model.DramaCast, reason string) error {
	applied, err := r.s.repo.UpdateDramaCastRevision(cast.ID, cast.Revision, map[string]any{"image_status": model.DramaMediaFailed})
	if err != nil || !applied {
		return err
	}
	cast.ImageStatus = model.DramaMediaFailed
	now := time.Now()
	job := model.DramaJob{
		ID: newID(), ProjectID: r.project.ID, Status: model.DramaJobFailed, Kind: model.DramaJobCastImage, CastID: cast.ID,
		Priority: castImagePriority(cast.Position), Label: truncateRunes("参考图："+cast.Name, 200), Error: truncateRunes(reason, 500),
		CreatedAt: now, UpdatedAt: now, FinishedAt: &now,
	}
	r.newJobs = append(r.newJobs, job)
	return nil
}
