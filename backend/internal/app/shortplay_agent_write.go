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

// W07 写面第一片：`shortplay_asset` 的 create/update（含解锁）。
//
// 只走既有审批链路：prepare 只做校验与预览、不落库；执行前重新 prepare，写入在 Agent
// 检查点事务内完成，任一步失败整体回滚。project 内整数 id 是身份，跨作品/跨账号一律拒绝。
// place/image/audio 未实现，因此不在 description、schema enum 或文档中披露为可用。

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
}

type cloudAgentShortplayAssetPlan struct {
	Args    cloudAgentShortplayAssetArgs
	Owner   *model.Shortplay
	Episode *model.ShortplayEpisode
	// Existing 非空表示 update 目标行。
	Existing *model.ShortplayAsset
	Preview  cloudAgentApprovalPreview
}

func cloudAgentShortplayAssetSchema() map[string]any {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return map[string]any{
		"action":      map[string]any{"type": "string", "enum": []string{"create", "update"}, "description": "create 建行；update 改既有行（含解锁）"},
		"assetId":     map[string]any{"type": "integer", "minimum": 0, "description": "update 必填：当前短剧项目内的整数资产 id"},
		"kind":        map[string]any{"type": "string", "enum": []string{"character", "prop", "scene"}, "description": "create 必填；update 不可改分类"},
		"name":        str("create 必填；update 留空不改"),
		"aliases":     map[string]any{"type": "array", "maxItems": 16, "items": str("别称")},
		"description": str("叙事设定；update 留空不改"),
		"appearance":  str("可直接生图的外貌描述；update 留空不改"),
		"voice":       str("仅角色使用的音色描述；留空不改"),
		"locked":      map[string]any{"type": "boolean", "description": "true 锁定防自动重做；false 解锁；省略不改"},
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

func prepareCloudAgentShortplayAsset(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentShortplayAssetPlan, error) {
	var args cloudAgentShortplayAssetArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	owner, episode, err := cloudAgentShortplayOwner(repo, userID, canvasID)
	if err != nil {
		return nil, err
	}
	plan := &cloudAgentShortplayAssetPlan{Args: args, Owner: owner, Episode: episode}
	switch args.Action {
	case "create":
		if strings.TrimSpace(args.Name) == "" {
			return nil, &cloudAgentArgumentError{BadAuthRequest("create 需要资产名称")}
		}
		if err := shortplay.ValidateAssetKind(args.Kind); err != nil {
			return nil, &cloudAgentArgumentError{BadAuthRequest(err.Error())}
		}
		plan.Preview = cloudAgentApprovalPreview{
			Kind: "canvas_mutation", Title: "确认新建短剧资产",
			Description: fmt.Sprintf("Agent 准备在短剧《%s》里新建%s资产《%s》，并在本集画布上放置资产卡。批准后才会执行。", owner.ProjectID, shortplay.AssetKindLabel(args.Kind), strings.TrimSpace(args.Name)),
			Items: []cloudAgentApprovalPreviewItem{{
				Operation: "create_shortplay_asset", NodeTitle: strings.TrimSpace(args.Name),
				NodeType: shortplayAssetNodeType, NodeTypeLabel: "短剧资产卡",
				Summary: fmt.Sprintf("新建%s《%s》", shortplay.AssetKindLabel(args.Kind), strings.TrimSpace(args.Name)),
			}},
		}
	case "update":
		if args.AssetID <= 0 {
			return nil, &cloudAgentArgumentError{BadAuthRequest("update 需要 assetId（项目内整数）")}
		}
		row, err := repo.ShortplayAssetByID(owner.ID, args.AssetID)
		if errors.Is(err, repository.ErrShortplayAssetNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &cloudAgentArgumentError{BadAuthRequest(fmt.Sprintf("资产 %d 不属于这部短剧", args.AssetID))}
		}
		if err != nil {
			return nil, err
		}
		if row.DerivedFrom != 0 {
			return nil, &cloudAgentArgumentError{BadAuthRequest("衍生造型继承父资产，请在资产库编辑；该工具只改基础资产")}
		}
		if args.Kind != "" && args.Kind != row.Kind {
			return nil, &cloudAgentArgumentError{BadAuthRequest("资产分类不可修改")}
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
	default:
		return nil, &cloudAgentArgumentError{BadAuthRequest("action 只支持 create 或 update")}
	}
	return plan, nil
}

// applyCloudAgentShortplayAsset 在检查点事务内建/改资产行、绑集并刷新资产卡。
func applyCloudAgentShortplayAsset(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting) (any, error) {
	plan, err := prepareCloudAgentShortplayAsset(repo, userID, canvasID, call)
	if err != nil {
		return nil, err
	}
	service := &Service{repo: repo}
	args := plan.Args
	var assetID int
	switch args.Action {
	case "create":
		assets, err := repo.ShortplayAssets(plan.Owner.ID)
		if err != nil {
			return nil, err
		}
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
	case "update":
		existing := plan.Existing
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
		assetID = existing.ID
	}
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
		"action": args.Action, "assetId": fresh.ID, "kind": fresh.Kind, "name": fresh.Name,
		"locked": fresh.Locked, "revision": fresh.Revision, "shortplayId": plan.Owner.ID,
	}, nil
}
