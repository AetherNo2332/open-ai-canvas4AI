package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W07 写面：`shortplay_asset` 的 create/update/place/image/audio。
//
// 只走既有审批链路：prepare 只做校验与预览、不落库；执行前重新 prepare，写入在 Agent
// 检查点事务内完成，任一步失败整体回滚。项目内整数 id 是身份，跨作品/跨账号一律拒绝。
// image/audio 只入队 job，不直接提交付费任务：提交、准入、计费与回调仍在调度器里。

type cloudAgentShortplayAssetArgs struct {
	Action      string   `json:"action"`
	AssetID     int      `json:"assetId"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
	Appearance  string   `json:"appearance"`
	Voice       string   `json:"voice"`
	Locked      *bool    `json:"locked"`
	AssetIDs    []int    `json:"assetIds"`
	OnlyMissing bool     `json:"onlyMissing"`
}

type cloudAgentShortplayAssetPlan struct {
	Args     cloudAgentShortplayAssetArgs
	Owner    *model.Shortplay
	Episode  *model.ShortplayEpisode
	Existing *model.ShortplayAsset
	Assets   []model.ShortplayAsset
	Preview  cloudAgentApprovalPreview
}

func cloudAgentShortplayAssetSchema() map[string]any {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return map[string]any{
		"action":  map[string]any{"type": "string", "enum": []string{"create", "update", "place", "image", "audio"}, "description": "create 建行；update 改设定/解锁；place 投送卡到本集；image/audio 入队生成"},
		"assetId": map[string]any{"type": "integer", "minimum": 0, "description": "update 必填：项目内整数资产 id"},
		"kind":    map[string]any{"type": "string", "enum": []string{"character", "prop", "scene"}, "description": "create 必填；update 不可改分类"},
		"name":    str("create 必填；update 留空不改"),
		"aliases": map[string]any{"type": "array", "maxItems": 16, "items": str("别称")},
		"description": str("叙事设定；update 留空不改"),
		"appearance":  str("可直接生图的外貌描述；update 留空不改"),
		"voice":       str("仅角色使用的音色描述；留空不改"),
		"locked":      map[string]any{"type": "boolean", "description": "true 锁定防自动重做；false 解锁；省略不改"},
		"assetIds":    map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "integer"}, "description": "place 必填；image/audio 可省略表示全部"},
		"onlyMissing": map[string]any{"type": "boolean", "description": "image/audio：只排从未生成过的资产"},
	}
}

// cloudAgentShortplayOwner 按画布反查作品与分集；自由画布与跨账号画布都拒绝。
func cloudAgentShortplayOwner(repo *repository.Repository, userID, canvasID string) (*model.Shortplay, *model.ShortplayEpisode, error) {
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, BadAuthRequest("当前画布不存在或不属于当前账号")
	}
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(canvas.ProjectID) == "" {
		return nil, nil, BadAuthRequest("当前画布没有作品容器；资产工具只能在短剧分集画布使用")
	}
	owner, err := repo.ShortplayByProject(canvas.ProjectID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, BadAuthRequest("当前画布不属于短剧作品；资产工具只能在短剧分集画布使用")
	}
	if err != nil {
		return nil, nil, err
	}
	if owner.UserID != userID {
		return nil, nil, BadAuthRequest("画布归属校验失败")
	}
	episode, err := repo.ShortplayEpisodeByCanvas(canvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return owner, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return owner, episode, nil
}

// cloudAgentShortplayRequestedAssets 校验请求里的资产 id 全部属于本作品，返回命中的行。
func cloudAgentShortplayRequestedAssets(assets []model.ShortplayAsset, ids []int) ([]model.ShortplayAsset, error) {
	byID := make(map[int]model.ShortplayAsset, len(assets))
	for _, row := range assets {
		byID[row.ID] = row
	}
	seen := make(map[int]bool, len(ids))
	result := make([]model.ShortplayAsset, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		row, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("资产 %d 不属于这部短剧", id)
		}
		seen[id] = true
		result = append(result, row)
	}
	return result, nil
}

func prepareCloudAgentShortplayAsset(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentShortplayAssetPlan, error) {
	var args cloudAgentShortplayAssetArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	owner, episode, err := cloudAgentShortplayOwner(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	assets, err := repo.ShortplayAssets(owner.ID)
	if err != nil {
		return nil, err
	}
	plan := &cloudAgentShortplayAssetPlan{Args: args, Owner: owner, Episode: episode}
	switch args.Action {
	case "create":
		return plan, prepareCloudAgentShortplayAssetCreate(plan)
	case "update":
		return plan, prepareCloudAgentShortplayAssetUpdate(plan, repo)
	case "place":
		return plan, prepareCloudAgentShortplayAssetPlace(plan, assets)
	case "image", "audio":
		return plan, prepareCloudAgentShortplayAssetMedia(plan, assets)
	default:
		return nil, &cloudAgentArgumentError{BadAuthRequest("action 只支持 create、update、place、image 或 audio")}
	}
}

func prepareCloudAgentShortplayAssetCreate(plan *cloudAgentShortplayAssetPlan) error {
	args := plan.Args
	if strings.TrimSpace(args.Name) == "" {
		return &cloudAgentArgumentError{BadAuthRequest("create 需要资产名称")}
	}
	if err := shortplay.ValidateAssetKind(args.Kind); err != nil {
		return &cloudAgentArgumentError{BadAuthRequest(err.Error())}
	}
	plan.Preview = cloudAgentApprovalPreview{
		Kind: "canvas_mutation", Title: "确认新建短剧资产",
		Description: fmt.Sprintf("Agent 准备在短剧《%s》里新建%s资产《%s》，并在本集画布上放置资产卡。批准后才会执行。", plan.Owner.ProjectID, shortplay.AssetKindLabel(args.Kind), strings.TrimSpace(args.Name)),
		Items: []cloudAgentApprovalPreviewItem{{
			Operation: "create_shortplay_asset", NodeTitle: strings.TrimSpace(args.Name),
			NodeType: shortplayAssetNodeType, NodeTypeLabel: "短剧资产卡",
			Summary: fmt.Sprintf("新建%s《%s》", shortplay.AssetKindLabel(args.Kind), strings.TrimSpace(args.Name)),
		}},
	}
	return nil
}

func prepareCloudAgentShortplayAssetUpdate(plan *cloudAgentShortplayAssetPlan, repo *repository.Repository) error {
	args := plan.Args
	if args.AssetID <= 0 {
		return &cloudAgentArgumentError{BadAuthRequest("update 需要 assetId（项目内整数）")}
	}
	row, err := repo.ShortplayAssetByID(plan.Owner.ID, args.AssetID)
	if errors.Is(err, repository.ErrShortplayAssetNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return &cloudAgentArgumentError{BadAuthRequest(fmt.Sprintf("资产 %d 不属于这部短剧", args.AssetID))}
	}
	if err != nil {
		return err
	}
	if row.DerivedFrom != 0 {
		return &cloudAgentArgumentError{BadAuthRequest("衍生造型继承父资产，请在资产库编辑；该工具只改基础资产")}
	}
	if args.Kind != "" && args.Kind != row.Kind {
		return &cloudAgentArgumentError{BadAuthRequest("资产分类不可修改")}
	}
	plan.Existing = row
	title := row.Name
	if strings.TrimSpace(args.Name) != "" {
		title = strings.TrimSpace(args.Name)
	}
	plan.Preview = cloudAgentApprovalPreview{
		Kind: "canvas_mutation", Title: "确认修改短剧资产",
		Description: fmt.Sprintf("Agent 准备修改短剧资产 #%d《%s》的设定，并刷新本集画布上的资产卡。批准后才会执行。", row.ID, title),
		Items: []cloudAgentApprovalPreviewItem{{
			Operation: "update_shortplay_asset", NodeTitle: title,
			NodeType: shortplayAssetNodeType, NodeTypeLabel: "短剧资产卡",
			Summary: fmt.Sprintf("修改资产 #%d《%s》", row.ID, title),
		}},
	}
	return nil
}

func prepareCloudAgentShortplayAssetPlace(plan *cloudAgentShortplayAssetPlan, assets []model.ShortplayAsset) error {
	if plan.Episode == nil {
		return &cloudAgentArgumentError{BadAuthRequest("place 只能在短剧分集画布使用")}
	}
	requested, err := cloudAgentShortplayRequestedAssets(assets, plan.Args.AssetIDs)
	if err != nil {
		return &cloudAgentArgumentError{err}
	}
	if len(requested) == 0 {
		return &cloudAgentArgumentError{BadAuthRequest("place 需要至少一个 assetId")}
	}
	names := make([]string, 0, len(requested))
	for _, row := range requested {
		names = append(names, row.Name)
	}
	plan.Preview = cloudAgentApprovalPreview{
		Kind: "canvas_mutation", Title: "确认投送资产卡",
		Description: fmt.Sprintf("Agent 准备把 %d 张资产卡投送到第 %d 集画布（%s）。已在本集的卡只刷新，不叠第二张。批准后才会执行。", len(requested), plan.Episode.Number, strings.Join(names, "、")),
		Items: []cloudAgentApprovalPreviewItem{{
			Operation: "place_shortplay_assets", NodeTypeLabel: "短剧资产卡",
			Summary: fmt.Sprintf("投送 %d 张资产卡到第 %d 集", len(requested), plan.Episode.Number),
		}},
	}
	return nil
}

func prepareCloudAgentShortplayAssetMedia(plan *cloudAgentShortplayAssetPlan, assets []model.ShortplayAsset) error {
	requested, err := cloudAgentShortplayRequestedAssets(assets, plan.Args.AssetIDs)
	if err != nil {
		return &cloudAgentArgumentError{err}
	}
	if len(plan.Args.AssetIDs) > 0 && len(requested) == 0 {
		return &cloudAgentArgumentError{BadAuthRequest("指定的资产都不属于这部短剧")}
	}
	label := "参考图"
	if plan.Args.Action == "audio" {
		label = "音色样本"
	}
	scope := "全部资产"
	if len(requested) > 0 {
		scope = fmt.Sprintf("%d 个指定资产", len(requested))
	}
	plan.Preview = cloudAgentApprovalPreview{
		Kind: "canvas_mutation", Title: "确认入队" + label,
		Description: fmt.Sprintf("Agent 准备为%s入队%s生成。只入队，不直接提交付费任务；任务提交、准入与计费仍由调度器执行。", scope, label),
		Items: []cloudAgentApprovalPreviewItem{{
			Operation: "queue_shortplay_" + plan.Args.Action, NodeTypeLabel: "短剧资产",
			Summary: fmt.Sprintf("入队%s：%s", label, scope),
		}},
	}
	return nil
}

// applyCloudAgentShortplayAsset 在检查点事务内执行批准后的动作。
func applyCloudAgentShortplayAsset(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting) (any, error) {
	plan, err := prepareCloudAgentShortplayAsset(repo, userID, canvasID, call)
	if err != nil {
		return nil, err
	}
	switch plan.Args.Action {
	case "create":
		return applyCloudAgentShortplayAssetCreate(repo, plan)
	case "update":
		return applyCloudAgentShortplayAssetUpdate(repo, plan)
	case "place":
		return applyCloudAgentShortplayAssetPlace(repo, userID, plan)
	case "image", "audio":
		return applyCloudAgentShortplayAssetMedia(repo, userID, plan)
	}
	return nil, BadAuthRequest("未知的短剧资产动作")
}

func applyCloudAgentShortplayAssetCreate(repo *repository.Repository, plan *cloudAgentShortplayAssetPlan) (any, error) {
	args := plan.Args
	assets, err := repo.ShortplayAssets(plan.Owner.ID)
	if err != nil {
		return nil, err
	}
	assetID := 0
	// 重放幂等：同分类同名只建一次，重复批准不会落第二行。
	for index := range assets {
		if assets[index].DerivedFrom == 0 && assets[index].Kind == args.Kind && strings.TrimSpace(assets[index].Name) == strings.TrimSpace(args.Name) {
			assetID = assets[index].ID
			break
		}
	}
	if assetID == 0 {
		row, buildErr := shortplay.NewBaseAssetRow(plan.Owner.ID, shortplay.NextAssetID(assets), shortplay.UpsertAssetRequest{
			Kind: args.Kind, Name: args.Name, Aliases: args.Aliases, Description: args.Description,
			Appearance: args.Appearance, Voice: args.Voice, Locked: args.Locked != nil && *args.Locked,
		})
		if buildErr != nil {
			return nil, &cloudAgentArgumentError{BadAuthRequest(buildErr.Error())}
		}
		row.Revision, row.Position = 1, nextShortplayAssetPosition(assets)
		row.EpisodeNumbersJSON = "[]"
		row.CreatedAt, row.UpdatedAt = time.Now(), time.Now()
		if err := repo.CreateShortplayAsset(&row); err != nil {
			return nil, err
		}
		assetID = row.ID
	}
	return finishCloudAgentShortplayAssetWrite(repo, plan, assetID)
}

func applyCloudAgentShortplayAssetUpdate(repo *repository.Repository, plan *cloudAgentShortplayAssetPlan) (any, error) {
	args, existing := plan.Args, plan.Existing
	locked := existing.Locked
	if args.Locked != nil {
		locked = *args.Locked
	}
	row, buildErr := shortplay.ApplyBaseAssetWrite(*existing, shortplay.UpsertAssetRequest{
		AssetID: existing.ID, Kind: existing.Kind, Name: args.Name, Aliases: args.Aliases,
		Description: args.Description, Appearance: args.Appearance, Voice: args.Voice, Locked: locked,
	})
	if buildErr != nil {
		return nil, &cloudAgentArgumentError{BadAuthRequest(buildErr.Error())}
	}
	applied, err := repo.UpdateShortplayAsset(&row, existing.Revision)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, NewAppError(409, "资产已被其他窗口修改，请重新读取后再试")
	}
	return finishCloudAgentShortplayAssetWrite(repo, plan, existing.ID)
}

func applyCloudAgentShortplayAssetPlace(repo *repository.Repository, userID string, plan *cloudAgentShortplayAssetPlan) (any, error) {
	service := &Service{repo: repo}
	result, err := service.PlaceShortplayAssetCards(userID, plan.Owner.ProjectID, plan.Episode.ID, shortplay.PlaceAssetRequest{AssetIDs: plan.Args.AssetIDs})
	if err != nil {
		return nil, err
	}
	return map[string]any{"action": "place", "placed": result.Placed, "nodeIds": result.NodeIDs, "episodeNumbers": result.EpisodeNumbers}, nil
}

func applyCloudAgentShortplayAssetMedia(repo *repository.Repository, userID string, plan *cloudAgentShortplayAssetPlan) (any, error) {
	service := &Service{repo: repo}
	if plan.Args.Action == "audio" {
		result, err := service.QueueShortplayAssetAudio(userID, plan.Owner.ProjectID, shortplay.QueueAssetAudioRequest{AssetIDs: plan.Args.AssetIDs, OnlyMissing: plan.Args.OnlyMissing})
		if err != nil {
			return nil, err
		}
		return map[string]any{"action": "audio", "queued": result.Queued, "reused": result.Reused, "skipped": result.Skipped, "jobIds": result.JobIDs, "note": "音色样本只入队；提交、准入与计费由调度器执行"}, nil
	}
	result, err := service.QueueShortplayAssetImages(userID, plan.Owner.ProjectID, shortplay.QueueAssetImagesRequest{AssetIDs: plan.Args.AssetIDs, OnlyMissing: plan.Args.OnlyMissing})
	if err != nil {
		return nil, err
	}
	return map[string]any{"action": "image", "queued": result.Queued, "reused": result.Reused, "skipped": result.Skipped, "jobIds": result.JobIDs, "note": "参考图只入队；提交、准入与计费由调度器执行"}, nil
}

// finishCloudAgentShortplayAssetWrite 绑集、刷新资产卡并返回回执。
func finishCloudAgentShortplayAssetWrite(repo *repository.Repository, plan *cloudAgentShortplayAssetPlan, assetID int) (any, error) {
	service := &Service{repo: repo}
	if plan.Episode != nil {
		if err := service.shortplayBindAssetsToEpisode(plan.Owner, plan.Episode, []int{assetID}); err != nil {
			return nil, err
		}
	}
	service.injectShortplayAssetCards(plan.Owner.ID)
	fresh, err := repo.ShortplayAssetByID(plan.Owner.ID, assetID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action": plan.Args.Action, "assetId": fresh.ID, "kind": fresh.Kind, "name": fresh.Name,
		"locked": fresh.Locked, "revision": fresh.Revision, "shortplayId": plan.Owner.ID,
	}, nil
}
