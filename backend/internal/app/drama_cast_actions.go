package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

func (s *Service) UpdateDramaCast(userID string, projectID string, castID string, req UpdateDramaCastRequest) (model.DramaCast, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return model.DramaCast{}, err
	}
	cast, err := s.repo.DramaCast(project.ID, castID)
	if err != nil {
		return model.DramaCast{}, err
	}
	fields := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || utf8.RuneCountInString(name) > 60 {
			return model.DramaCast{}, BadAuthRequest("名称需为 1 到 60 字")
		}
		fields["name"] = name
	}
	if req.Aliases != nil {
		aliases := []string{}
		for _, alias := range *req.Aliases {
			if alias = strings.TrimSpace(alias); alias != "" && len(aliases) < 20 {
				aliases = append(aliases, truncateRunes(alias, 40))
			}
		}
		encoded, _ := json.Marshal(aliases)
		fields["aliases_json"] = string(encoded)
	}
	for _, field := range []struct {
		column string
		value  *string
		limit  int
	}{{"appearance", req.Appearance, 1000}, {"description", req.Description, 1000}, {"voice_instructions", req.VoiceInstructions, 500}} {
		if field.value != nil {
			fields[field.column] = truncateRunes(strings.TrimSpace(*field.value), field.limit)
		}
	}
	if req.Locked != nil {
		fields["locked"] = *req.Locked
	}
	if len(fields) > 0 {
		if err := s.repo.UpdateDramaCastFields(cast.ID, fields); err != nil {
			return model.DramaCast{}, err
		}
	}
	updated, err := s.repo.DramaCast(project.ID, castID)
	if err != nil {
		return model.DramaCast{}, err
	}
	return *updated, nil
}

// AddDramaCastLook 为角色新增一套造型：造型是角色的子行，只承载这套造型的名称、外观与默认适用章节；
// 名称、别名与声音沿用所属角色。新造型只保存为草稿，用户单独请求生成后才进入调度。
func (s *Service) AddDramaCastLook(userID, projectID, castID string, req DramaCastLookRequest) (map[string]any, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	character, err := s.repo.DramaCast(project.ID, castID)
	if err != nil {
		return nil, err
	}
	if character.Kind != "character" || character.ParentID != "" {
		return nil, BadAuthRequest("只能为角色添加造型")
	}
	name := strings.TrimSpace(req.LookName)
	appearance := strings.TrimSpace(req.Appearance)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return nil, BadAuthRequest("造型名需为 1 到 60 字")
	}
	if production.IsBaseLookName(name) {
		return nil, BadAuthRequest("造型名不能与基础造型重名")
	}
	if appearance == "" {
		return nil, BadAuthRequest("请填写这套造型的外观描述")
	}
	existing, err := s.repo.DramaCasts(project.ID)
	if err != nil {
		return nil, err
	}
	looks := dramaLooksForCharacter(existing, character.ID)
	if len(looks) >= production.MaxCharacterLooks {
		return nil, BadAuthRequest(fmt.Sprintf("每个角色最多 %d 套造型", production.MaxCharacterLooks))
	}
	from, to := dramaNormalizeLookRange(req.FromChapter, req.ToChapter)
	look := model.DramaCast{ID: newID(), ProjectID: project.ID, Kind: "character", ParentID: character.ID, Name: character.Name, LookName: truncateRunes(name, 60), Appearance: truncateRunes(appearance, 1000), LookFromChapter: from, LookToChapter: to, Position: character.Position*100 + len(looks) + 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	look.ImageStatus = model.DramaMediaDraft
	if req.ImagePrompt != nil {
		look.ImagePrompt = strings.TrimSpace(*req.ImagePrompt)
		if utf8.RuneCountInString(look.ImagePrompt) > 12000 {
			return nil, BadAuthRequest("造型提示词最多 12000 字")
		}
	}
	if err := s.repo.CreateDramaCasts([]model.DramaCast{look}); err != nil {
		return nil, err
	}
	return map[string]any{"look": look}, nil
}

// UpdateDramaCastLook 只保存造型设定；外观变化可按请求标记下游视频过期，图片始终由用户单独请求生成。
func (s *Service) UpdateDramaCastLook(userID, projectID, lookID string, req DramaCastLookRequest) (map[string]any, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	look, err := s.repo.DramaCast(project.ID, lookID)
	if err != nil {
		return nil, err
	}
	if look.ParentID == "" {
		return nil, BadAuthRequest("只能修改造型")
	}
	if look.ImageStatus == model.DramaMediaQueued || look.ImageStatus == model.DramaMediaRunning {
		return nil, BadAuthRequest("造型正在生成，请完成后再修改")
	}
	fields, err := dramaCastLookUpdateFields(look, req)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return map[string]any{"look": look}, nil
	}
	// 保存只更新设定；参考图保留到用户明确生成或替换图片。
	if err := s.repo.UpdateDramaCastFields(look.ID, fields); err != nil {
		return nil, err
	}
	affected := 0
	if _, ok := fields["appearance"]; ok && req.RestyleSegments {
		if affected, err = s.restyleDramaCastSegments(project.ID, look.ID); err != nil {
			return nil, err
		}
	}
	updated, err := s.repo.DramaCast(project.ID, lookID)
	if err != nil {
		return nil, err
	}
	s.wakeDramaScheduler()
	return map[string]any{"look": updated, "affectedSegments": affected}, nil
}

// DeleteDramaCastLook 删除一套造型；引用它的镜头回退到角色本体，受影响的剧情段视频过期需重做。
func (s *Service) DeleteDramaCastLook(userID, projectID, lookID string, restyleSegments bool) (map[string]any, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	look, err := s.repo.DramaCast(project.ID, lookID)
	if err != nil {
		return nil, err
	}
	if look.ParentID == "" {
		return nil, BadAuthRequest("只能删除造型")
	}
	affected := 0
	if restyleSegments {
		if affected, err = s.restyleDramaCastSegments(project.ID, look.ID); err != nil {
			return nil, err
		}
	}
	if err := s.repo.DeleteDramaCastLook(project.ID, lookID); err != nil {
		return nil, err
	}
	s.wakeDramaScheduler()
	return map[string]any{"affectedSegments": affected}, nil
}

// dramaNormalizeLookRange 归一造型的默认适用章节：负数按 0（不限），起止颠倒时交换。
func dramaNormalizeLookRange(from, to *int) (int, int) {
	fromValue, toValue := 0, 0
	if from != nil {
		fromValue = max(*from, 0)
	}
	if to != nil {
		toValue = max(*to, 0)
	}
	if fromValue > 0 && toValue > 0 && fromValue > toValue {
		fromValue, toValue = toValue, fromValue
	}
	return fromValue, toValue
}

// SetDramaCastVoice 为角色指定声音：从声音库选一条带样本音频的声音，或用刚上传 / 录制的样本（自动登记到声音库）。
// clear=true 时清除，只按声线描述生成。视频模型支持参考音频时，该角色说话的剧情段会带上这段样本；
// restyleSegments=true 时让引用该角色的剧情段视频过期，需重新生成。造型沿用所属角色的声音，不能单独指定。
func (s *Service) SetDramaCastVoice(userID, projectID, castID string, req SetDramaCastVoiceRequest, clear bool) (map[string]any, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	cast, err := s.repo.DramaCast(project.ID, castID)
	if err != nil {
		return nil, err
	}
	if cast.Kind != "character" || cast.ParentID != "" {
		return nil, BadAuthRequest("只能为角色指定声音，造型沿用所属角色的声音")
	}
	fields := map[string]any{"voice_profile_id": "", "voice_sample_resource_id": ""}
	if !clear {
		profile, err := s.resolveVoiceProfile(userID, req.VoiceProfileID, req.SampleResourceID, firstNonEmpty(req.VoiceName, cast.Name+"的声音"))
		if err != nil {
			return nil, err
		}
		// 视频模型只能参考真实音频；声音库里只有音色名、没有样本的内置声音无法随视频请求发送。
		if strings.TrimSpace(profile.SampleResourceID) == "" {
			return nil, BadAuthRequest("这个声音没有样本音频，视频模型无法参考；请上传或录制一段角色的声音")
		}
		fields["voice_profile_id"], fields["voice_sample_resource_id"] = profile.ID, profile.SampleResourceID
	}
	if err := s.repo.UpdateDramaCastFields(cast.ID, fields); err != nil {
		return nil, err
	}
	affected := 0
	if req.RestyleSegments {
		if affected, err = s.restyleDramaCastSegments(project.ID, cast.ID); err != nil {
			return nil, err
		}
	}
	updated, err := s.repo.DramaCast(project.ID, castID)
	if err != nil {
		return nil, err
	}
	s.wakeDramaScheduler()
	return map[string]any{"cast": updated, "affectedSegments": affected}, nil
}

// RegenerateDramaCast 重做参考图；restyleSegments=true 时让引用该设定的剧情段视频过期，需重新生成。
// 段提示词只引用“参考图N”编号，不随图片内容变化，保持不变。
func (s *Service) RegenerateDramaCast(userID string, projectID string, castID string, restyleSegments bool) (map[string]any, error) {
	project, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	cast, err := s.repo.DramaCast(project.ID, castID)
	if err != nil {
		return nil, err
	}
	if cast.Locked {
		return nil, BadAuthRequest("设定已锁定，请先解锁")
	}
	// queued 表示用户明确请求：手动生图模式下即使资产尚未整体放行也会生成。
	if err := s.repo.UpdateDramaCastFields(cast.ID, map[string]any{"image_status": model.DramaMediaQueued}); err != nil {
		return nil, err
	}
	// 重试角色本体时，把它因本体失败而落败的造型一起重新排队。
	if cast.ParentID == "" {
		if err := s.requeueFailedDramaLooks(project.ID, cast.ID); err != nil {
			return nil, err
		}
	}
	affected := 0
	if restyleSegments {
		if affected, err = s.restyleDramaCastSegments(project.ID, cast.ID); err != nil {
			return nil, err
		}
	}
	if err := s.requestDramaWork(productionRow); err != nil {
		return nil, err
	}
	return map[string]any{"affectedSegments": affected}, nil
}

// restyleDramaCastSegments 让引用该设定、已生成或生成中的剧情段视频过期，并把所在集退回视频阶段。返回过期的段数。
func (s *Service) restyleDramaCastSegments(projectID, castID string) (int, error) {
	shots, err := s.repo.DramaShotsReferencingCast(projectID, castID)
	if err != nil {
		return 0, err
	}
	rewind := s.newEpisodeRewind(projectID)
	marked := map[string]bool{}
	affected := 0
	for _, shot := range shots {
		key := shot.EpisodeID + "|" + itoa(shot.SegmentIndex)
		if marked[key] {
			continue
		}
		marked[key] = true
		changed, err := s.repo.MarkDramaSegmentVideoStale(shot.EpisodeID, shot.SegmentIndex)
		if err != nil {
			return 0, err
		}
		if !changed {
			continue
		}
		if err := rewind.to(shot.EpisodeID, model.DramaEpisodeVideo); err != nil {
			return 0, err
		}
		affected++
	}
	return affected, rewind.commit()
}

// DramaAssetSchemeCatalog 是参考图方案目录：内置方案的默认提示词与可用变量。
type DramaAssetSchemeCatalog struct {
	Schemes            []production.AssetScheme `json:"schemes"`
	ScenePrompt        string                   `json:"scenePrompt"`
	CharacterVariables []string                 `json:"characterVariables"`
	SceneVariables     []string                 `json:"sceneVariables"`
}

func (s *Service) DramaAssetSchemes() DramaAssetSchemeCatalog {
	return DramaAssetSchemeCatalog{Schemes: production.AssetSchemes(), ScenePrompt: production.DefaultScenePrompt(), CharacterVariables: production.CharacterAssetVariables, SceneVariables: production.SceneAssetVariables}
}

type RegenerateDramaCastsRequest struct {
	Kind            string `json:"kind"`
	RestyleSegments bool   `json:"restyleSegments"`
}

// RegenerateDramaCasts 按当前参考图方案重做某一类（角色/道具/场景）全部未锁定的参考图（换方案后使用）。
func (s *Service) RegenerateDramaCasts(userID string, projectID string, req RegenerateDramaCastsRequest) (map[string]any, error) {
	if req.Kind != "character" && req.Kind != "prop" && req.Kind != "scene" {
		return nil, BadAuthRequest("请选择要重做的是角色、道具还是场景")
	}
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	casts, err := s.repo.DramaCasts(project.ID)
	if err != nil {
		return nil, err
	}
	casted, skipped, shots := 0, 0, 0
	for _, cast := range casts {
		if cast.Kind != req.Kind {
			continue
		}
		if cast.Locked || cast.ImageStatus == model.DramaMediaQueued || cast.ImageStatus == model.DramaMediaRunning {
			skipped++
			continue
		}
		result, err := s.RegenerateDramaCast(userID, projectID, cast.ID, req.RestyleSegments)
		if err != nil {
			return nil, err
		}
		if affected, ok := result["affectedSegments"].(int); ok {
			shots += affected
		}
		casted++
	}
	return map[string]any{"queued": casted, "skipped": skipped, "affectedSegments": shots}, nil
}

// DramaCastImpact 预览修改某个设定会影响多少集和镜头。
func (s *Service) DramaCastImpact(userID string, projectID string, castID string) (map[string]any, error) {
	project, _, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	shots, err := s.repo.DramaShotsReferencingCast(project.ID, castID)
	if err != nil {
		return nil, err
	}
	episodes, segments := map[string]bool{}, map[string]bool{}
	for _, shot := range shots {
		episodes[shot.EpisodeID] = true
		segments[shot.EpisodeID+"|"+itoa(shot.SegmentIndex)] = true
	}
	return map[string]any{"segments": len(segments), "episodes": len(episodes)}, nil
}

// RetryFailedDrama 把失败槽位重置为待生成，由调度循环重新派发。
func (s *Service) RetryFailedDrama(userID, projectID string) (map[string]any, error) {
	project, p, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	segments, err := s.repo.FailedDramaSegments(project.ID, 5000)
	if err != nil {
		return nil, err
	}
	for _, seg := range segments {
		fields := map[string]any{}
		if seg.PromptStatus == model.DramaMediaFailed {
			fields["prompt_status"] = ""
			fields["prompt_error"] = ""
		}
		if seg.VideoStatus == model.DramaMediaFailed {
			fields["video_error"] = ""
			if seg.VideoResourceID == "" {
				fields["video_status"] = model.DramaMediaQueued
			} else {
				fields["video_status"] = model.DramaMediaRunning
			}
		}
		if err := s.repo.UpdateDramaSegmentFields(seg.ID, fields); err != nil {
			return nil, err
		}
	}
	if err := s.repo.RetryDramaResultJobs(project.ID); err != nil {
		return nil, err
	}
	casts, err := s.repo.DramaCasts(project.ID)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, cast := range casts {
		applied, err := s.repo.UpdateDramaCastIf(cast.ID, []string{model.DramaMediaFailed}, map[string]any{"image_status": model.DramaMediaQueued})
		if err != nil {
			return nil, err
		}
		if applied {
			count++
		}
	}
	episodes, err := s.repo.DramaEpisodes(project.ID)
	if err != nil {
		return nil, err
	}
	for _, episode := range episodes {
		if episode.Error != "" {
			if err := s.repo.UpdateDramaEpisodeUser(episode.ID, map[string]any{"error": ""}); err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{"segments": len(segments), "casts": count}, s.requestDramaWork(p)
}

func dramaCastLookUpdateFields(look *model.DramaCast, req DramaCastLookRequest) (map[string]any, error) {
	fields := map[string]any{}
	if strings.TrimSpace(req.LookName) != "" {
		name := strings.TrimSpace(req.LookName)
		if utf8.RuneCountInString(name) > 60 || production.IsBaseLookName(name) {
			return nil, BadAuthRequest("造型名无效")
		}
		fields["look_name"] = name
	}
	if req.Appearance != "" && strings.TrimSpace(req.Appearance) != look.Appearance {
		if strings.TrimSpace(req.Appearance) == "" {
			return nil, BadAuthRequest("请填写这套造型的外观描述")
		}
		fields["appearance"] = truncateRunes(strings.TrimSpace(req.Appearance), 1000)
	}
	if req.ImagePrompt != nil {
		prompt := strings.TrimSpace(*req.ImagePrompt)
		if utf8.RuneCountInString(prompt) > 12000 {
			return nil, BadAuthRequest("造型提示词最多 12000 字")
		}
		fields["image_prompt"] = prompt
	}
	if req.FromChapter != nil || req.ToChapter != nil {
		from, to := dramaNormalizeLookRange(req.FromChapter, req.ToChapter)
		fields["look_from_chapter"], fields["look_to_chapter"] = from, to
	}
	return fields, nil
}
