package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

// applyScriptShots 把镜头表写入本集：解析角色、道具与场景引用，按分镜规则计时并切分剧情段。
// hints 是剧本给出的新设定外观，用于即时建档；info 补齐逐集追加时缺少的梗概。
// imported 表示镜头来自用户分镜：写明的时长照写执行，只有模型写的剧本才按镜头节奏夹紧。
func (r *dramaRun) applyScriptShots(episode *model.DramaEpisode, scripted []production.ScriptShot, hints []production.ScriptCast, info production.ScriptEpisode, imported bool) error {
	shotMs := r.settings.Storyboard.ShotMs
	if imported {
		shotMs = r.settings.Storyboard.ImportedShotMs
	}
	casts, err := r.s.repo.DramaCasts(r.project.ID)
	if err != nil {
		return err
	}
	// 本集对应的原著章节序号（从 1 起），用于按章节选默认造型。
	chapterNumbers, err := r.dramaEpisodeChapterNumbers(episode)
	if err != nil {
		return err
	}
	hintByKey := map[string]production.ScriptCast{}
	lookHints := map[string]production.ScriptCast{}
	for _, hint := range hints {
		if hint.Kind == "look" {
			lookHints[production.NormalizeName(hint.Name)+"|"+production.NormalizeName(hint.Look)] = hint
			continue
		}
		hintByKey[hint.Kind+"|"+production.NormalizeName(hint.Name)] = hint
	}
	characters, props, scenes := dramaKindIndex(casts, "character"), dramaKindIndex(casts, "prop"), dramaKindIndex(casts, "scene")
	now := time.Now()
	var created []model.DramaCast
	position := len(casts)
	// looksByCharacter 既含作品圣经建的造型，也含本集剧本即时建的造型；按名称找本镜要用的造型行。
	looksByCharacter := map[string][]model.DramaCast{}
	for _, cast := range casts {
		if cast.Kind == "character" && cast.ParentID != "" {
			looksByCharacter[cast.ParentID] = append(looksByCharacter[cast.ParentID], cast)
		}
	}
	resolve := func(index *production.CastIndex, kind string, name string) string {
		name = strings.TrimSpace(name)
		if name == "" || production.IsNarrator(name) {
			return ""
		}
		if id, ok := index.Lookup(name); ok {
			return id
		}
		// 剧本里首次出现的人物或地点即时建档，后续集通过别名归并复用；剧本给了外观就一并写入，参考图才有依据。
		cast := model.DramaCast{ID: newID(), ProjectID: r.project.ID, Kind: kind, Name: truncateRunes(name, 60), AliasesJSON: "[]", Position: position, CreatedAt: now, UpdatedAt: now}
		if hint, ok := hintByKey[kind+"|"+production.NormalizeName(name)]; ok {
			cast.Appearance = truncateRunes(hint.Appearance, 1000)
			if kind == "character" {
				cast.VoiceInstructions = truncateRunes(hint.Voice, 500)
			}
		}
		position++
		created = append(created, cast)
		index.Add(cast.ID, cast.Name)
		return cast.ID
	}
	// resolveLook 为出镜角色挑出本镜造型行 ID：先看镜头指定的造型名，再看本集默认造型；
	// 剧本给了新造型名（looks 里有、设定表没有）时即时建一行造型。chapters 是本集章节序号。
	resolveLook := func(characterID, chosen string) string {
		if characterID == "" {
			return ""
		}
		existing := looksByCharacter[characterID]
		if id := dramaResolveShotLook(model.DramaCast{ID: characterID}, existing, chosen, chapterNumbers); id != "" {
			return id
		}
		name := strings.TrimSpace(chosen)
		if name == "" || production.IsBaseLookName(name) {
			return ""
		}
		hint, ok := lookHints[dramaLookHintKey(casts, characterID)+"|"+production.NormalizeName(name)]
		if !ok || strings.TrimSpace(hint.Appearance) == "" {
			return ""
		}
		appearanceKey := production.NormalizeName(hint.Appearance)
		for _, cast := range append(append([]model.DramaCast{}, casts...), created...) {
			if cast.ID == characterID && production.NormalizeName(cast.Appearance) == appearanceKey {
				return ""
			}
			if cast.ParentID == characterID && production.NormalizeName(cast.Appearance) == appearanceKey {
				return cast.ID
			}
		}
		look := model.DramaCast{ID: newID(), ProjectID: r.project.ID, Kind: "character", ParentID: characterID, Name: dramaCastName(casts, created, characterID), LookName: truncateRunes(name, 60), Appearance: truncateRunes(hint.Appearance, 1000), Position: position, CreatedAt: now, UpdatedAt: now}
		position++
		created = append(created, look)
		looksByCharacter[characterID] = append(looksByCharacter[characterID], look)
		return look.ID
	}
	shots := make([]model.DramaShot, 0, len(scripted))
	for index, item := range scripted {
		// 剧本列出的人物与道具全部挂到镜头上；每段实际发几张参考图由“每段最多参考图”和模型上限决定，人物优先。
		castIDs := []string{}
		lookIDs := []string{}
		resolveCharacter := func(name string) {
			id := resolve(characters, "character", name)
			if id == "" || containsString(castIDs, id) {
				return
			}
			castIDs = append(castIDs, id)
			if lookID := resolveLook(id, item.Looks[strings.TrimSpace(name)]); lookID != "" && !containsString(lookIDs, lookID) {
				lookIDs = append(lookIDs, lookID)
			}
		}
		for _, name := range item.Characters {
			resolveCharacter(name)
		}
		resolveCharacter(item.Speaker)
		propIDs := []string{}
		for _, name := range item.Props {
			if id := resolve(props, "prop", name); id != "" && !containsString(propIDs, id) {
				propIDs = append(propIDs, id)
			}
		}
		shot := model.DramaShot{ID: newID(), ProjectID: r.project.ID, EpisodeID: episode.ID, Position: index, Scene: truncateRunes(item.Scene, 120), SceneCastID: resolve(scenes, "scene", item.Scene), CastIDsJSON: encodeStringList(castIDs), LookCastIDsJSON: encodeStringList(lookIDs), PropIDsJSON: encodeStringList(propIDs), Speaker: truncateRunes(item.Speaker, 60), Dialogue: truncateRunes(item.Dialogue, dramaMaxDialogueRunes), Action: truncateRunes(item.Action, 1000), ShotSize: truncateRunes(item.ShotSize, 30), Camera: truncateRunes(item.Camera, 60), DurationMs: shotMs(item.DurationSeconds, item.Dialogue), ImagePrompt: truncateRunes(item.ImagePrompt, 2000), VideoPrompt: truncateRunes(item.VideoPrompt, 2000), SourceQuote: truncateRunes(item.SourceQuote, 300), Transition: production.NormalizeTransition(item.Transition), CreatedAt: now, UpdatedAt: now}
		shots = append(shots, shot)
	}
	return r.persistDramaScriptShots(episode, shots, created, info)
}

// dramaMaxDialogueRunes 是单镜台词的存储上限；远高于分镜规则的单镜字数，只防异常输入，不用于裁剪正常台词。
const dramaMaxDialogueRunes = 500

// dramaEpisodeChapterNumbers 返回本集引用的原著章节序号（从 1 起，即 position+1），按序号升序去重。
func (r *dramaRun) dramaEpisodeChapterNumbers(episode *model.DramaEpisode) ([]int, error) {
	ids := decodeStringList(episode.ChapterIDsJSON)
	if len(ids) == 0 {
		return nil, nil
	}
	units, err := r.s.repo.DramaChaptersByIDs(r.project.ID, ids)
	if err != nil {
		return nil, err
	}
	numbers := make([]int, 0, len(units))
	for _, unit := range units {
		numbers = append(numbers, unit.Position+1)
	}
	return numbers, nil
}

// dramaLookHintKey 返回角色的标准名归一后作为造型提示键的前缀；造型提示按角色名 + 造型名归并。
func dramaLookHintKey(casts []model.DramaCast, characterID string) string {
	for _, cast := range casts {
		if cast.ID == characterID {
			return production.NormalizeName(cast.Name)
		}
	}
	return ""
}

// dramaCastName 在已入库与本轮新建的设定里找角色名，供造型行沿用。
func dramaCastName(casts, created []model.DramaCast, characterID string) string {
	for _, cast := range append(append([]model.DramaCast{}, casts...), created...) {
		if cast.ID == characterID {
			return cast.Name
		}
	}
	return ""
}

func dramaKindIndex(casts []model.DramaCast, kind string) *production.CastIndex {
	index := production.NewCastIndex()
	for _, cast := range casts {
		if cast.Kind == kind {
			index.Add(cast.ID, append([]string{cast.Name}, decodeStringList(cast.AliasesJSON)...)...)
		}
	}
	return index
}

// dramaTaskResource 从任务结果中取第一个媒体资源 ID；兼容 storageKey 与裸 resourceId 两种写法。
func dramaTaskResource(task *model.Task) string {
	if id, _ := taskOutputResource(task.ResultJSON, task.Type); id != "" {
		return id
	}
	var value any
	if json.Unmarshal([]byte(task.ResultJSON), &value) != nil {
		return ""
	}
	var find func(any) string
	find = func(node any) string {
		switch item := node.(type) {
		case map[string]any:
			if id, ok := item["resourceId"].(string); ok && strings.TrimSpace(id) != "" {
				return strings.TrimSpace(id)
			}
			for _, child := range item {
				if id := find(child); id != "" {
					return id
				}
			}
		case []any:
			for _, child := range item {
				if id := find(child); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return find(value)
}

func (r *dramaRun) applyCastImage(job *model.DramaJob, task *model.Task) error {
	resourceID := dramaTaskResource(task)
	if resourceID == "" {
		return errors.New("参考图任务没有返回图片")
	}
	// 版本始终入库；只有槽位仍在等待本任务时才切换当前参考图，用户期间发起的重做不会被旧结果覆盖。
	if err := r.s.repo.CreateDramaTake(&model.DramaTake{ID: newID(), ProjectID: r.project.ID, OwnerID: job.CastID, Kind: "cast", ResourceID: resourceID, TaskID: task.ID, Prompt: truncateRunes(task.Prompt, 2000), CreatedAt: time.Now()}); err != nil {
		return err
	}
	// 有的模型会无视尺寸参数出方图：画幅明显不符时不设为当前参考图（否则会带进每个剧情段），
	// 结果保留在版本记录里，设定标记失败并写明实际尺寸。
	if cast, err := r.s.repo.DramaCast(r.project.ID, job.CastID); err == nil {
		ratio := r.castImageRatio(cast.Kind)
		if resource, err := r.s.repo.ResourceForUser(r.project.UserID, resourceID); err == nil && resource != nil && dramaImageRatioMismatch(resource.Width, resource.Height, ratio) {
			return &dramaResultIssue{message: fmt.Sprintf("图片模型返回了 %d×%d（%s）的参考图，与要求的 %s 不符；结果已保留在版本记录中，请换用支持 %s 的图片模型后重新生成", resource.Width, resource.Height, dramaRatioLabel(fmt.Sprintf("%dx%d", resource.Width, resource.Height)), ratio, ratio)}
		}
	}
	_, err := r.s.repo.UpdateDramaCastIf(job.CastID, []string{model.DramaMediaRunning, model.DramaMediaQueued}, map[string]any{"image_resource_id": resourceID, "image_status": model.DramaMediaReady, "image_version": gorm.Expr("image_version + 1")})
	return err
}

func (r *dramaRun) applyRender(job *model.DramaJob, task *model.Task) error {
	var result timelineRenderResult
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil || result.ResourceID == "" {
		return errors.New("成片渲染没有返回视频")
	}
	episode, err := r.s.repo.DramaEpisode(r.project.ID, job.EpisodeID)
	if err != nil {
		return err
	}
	if err := r.s.repo.CreateDramaTake(&model.DramaTake{ID: newID(), ProjectID: r.project.ID, OwnerID: episode.ID, Kind: "final", ResourceID: result.ResourceID, TaskID: task.ID, DurationMs: result.DurationMs, CreatedAt: time.Now()}); err != nil {
		return err
	}
	var payload dramaRevisionPayload
	_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
	if episode.Stage != model.DramaEpisodeRender || episode.Revision != payload.Revision {
		return nil
	}
	_, err = r.s.repo.UpdateDramaEpisodeIfRevision(episode.ID, episode.Revision, map[string]any{"final_resource_id": result.ResourceID, "subtitle_srt": result.SubtitleSRT, "final_stale": false, "duration_ms": result.DurationMs, "stage": model.DramaEpisodeDone, "error": ""})
	return err
}

// ===== 任务构建 =====

func (r *dramaRun) persistDramaScriptShots(episode *model.DramaEpisode, shots []model.DramaShot, created []model.DramaCast, info production.ScriptEpisode) error {
	profile, err := r.s.dramaVideoProfile(r.settings.Models.Video)
	if err != nil {
		return err
	}
	// 说完长台词的镜头可能超过视频模型单次上限：夹到上限而不是整集失败，台词保持原样，用户可在剧本里拆镜。
	limit := int64(profile.maxSeconds()) * 1000
	var total int64
	for i := range shots {
		if limit > 0 && shots[i].DurationMs > limit {
			shots[i].DurationMs = limit
		}
		total += shots[i].DurationMs
	}
	assignDramaSegments(shots, r.settings.Storyboard.SegmentLimits(profile.Durations))
	if err := r.s.repo.CreateDramaCasts(created); err != nil {
		return err
	}
	fields := map[string]any{"shot_count": len(shots), "duration_ms": total, "stage": model.DramaEpisodePrompts, "error": "", "approved": false}
	if strings.TrimSpace(episode.Synopsis) == "" && info.Synopsis != "" {
		fields["synopsis"], fields["hook"], fields["cliffhanger"] = truncateRunes(info.Synopsis, 1000), truncateRunes(info.Hook, 300), truncateRunes(info.Cliffhanger, 300)
	}
	revision := episode.Revision
	_, err = r.s.repo.ApplyDramaScript(episode.ID, &revision, fields, shots)
	return err
}
