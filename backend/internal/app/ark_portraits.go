// 方舟素材库：真人 / 虚拟 / 版权 IP 三类素材组，全部建在管理员配置的平台方舟账号下。
//   - 真人：本人完成 H5 活体认证后由方舟生成 LivenessFace 素材组，带授权到期时间。
//   - 虚拟：直接创建 AIGC 素材组。
//   - 版权 IP：只能由管理员创建或登记，再授权给用户只读使用。
//
// 用户自建的组（scope=user）只属于本人；管理员建的平台组（scope=platform）按「全部用户 / 指定用户」授权。
// 用户上传的素材按配置先经平台审核，管理员通过后才提交方舟；方舟审核通过后写入素材库「方舟认证」分类。
//
// 安全边界：
//   - 回调地址只带一次性随机 state，不带登录态；BytedToken 加密保存且只在服务端换取 GroupId。
//   - 任务引用他人个人组、未授权平台组或已过期真人组的素材 ID 时拒绝执行（validateArkPortraitReferences）。
//   - 登记方舟控制台已有的素材组只允许管理员操作，避免普通用户认领同账号下他人的组。
//   - 用户自建素材组的费用先冻结后结算：真人认证成功、虚拟组创建成功才扣费，失败、过期或取消自动退回。

package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const (
	arkGroupKindReal    = "real"
	arkGroupKindVirtual = "virtual"
	arkGroupKindIP      = "ip"

	arkGroupScopeUser     = "user"
	arkGroupScopePlatform = "platform"

	arkGroupSourceLiveness = "liveness"
	arkGroupSourceCreated  = "created"
	arkGroupSourceManual   = "manual"

	arkPortraitGroupType          = "LivenessFace"
	arkPortraitSessionTTL         = 30 * time.Minute
	arkPortraitSuccessCode        = "10000"
	arkPortraitSessionPending     = "pending"
	arkPortraitSessionVerified    = "verified"
	arkPortraitSessionFailed      = "failed"
	arkPortraitSessionExpired     = "expired"
	arkPortraitSessionCancelled   = "cancelled"
	arkPortraitAssetPendingReview = "pending_review"
	arkPortraitAssetRejected      = "rejected"
	arkPortraitAssetProcessing    = "processing"
	arkPortraitAssetActive        = "active"
	arkPortraitAssetFailed        = "failed"
	maxArkPortraitSessionsPerHour = 20
	maxArkPortraitAssetsPerGroup  = 200
	arkPortraitExpiringWindow     = 30 * 24 * time.Hour
	arkPortraitResultPath         = "/ark-portrait/result"
)

var (
	arkPortraitGroupIDPattern = regexp.MustCompile(`^group-[A-Za-z0-9-]{1,100}$`)
	arkPortraitStatePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
)

type ArkAssetConfigView struct {
	RealEnabled                   bool  `json:"realEnabled"`
	VirtualEnabled                bool  `json:"virtualEnabled"`
	RealGroupLimit                int   `json:"realGroupLimit"`
	VirtualGroupLimit             int   `json:"virtualGroupLimit"`
	BillingEnabled                bool  `json:"billingEnabled"`
	RealGroupPriceMicrocredits    int64 `json:"realGroupPriceMicrocredits"`
	VirtualGroupPriceMicrocredits int64 `json:"virtualGroupPriceMicrocredits"`
	RealReviewRequired            bool  `json:"realReviewRequired"`
	VirtualReviewRequired         bool  `json:"virtualReviewRequired"`
	RealAuthorizationDays         int   `json:"realAuthorizationDays"`
}

type ArkAssetUsageView struct {
	Real    int64 `json:"real"`
	Virtual int64 `json:"virtual"`
}

type ArkPortraitOverview struct {
	Enabled  bool                     `json:"enabled"`
	Site     string                   `json:"site,omitempty"`
	Config   ArkAssetConfigView       `json:"config"`
	Usage    ArkAssetUsageView        `json:"usage"`
	Groups   []ArkPortraitGroupView   `json:"groups"`
	Sessions []ArkPortraitSessionView `json:"sessions"`
}

type ArkUserSummary struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

type ArkPortraitGroupView struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Kind         string                 `json:"kind"`
	Scope        string                 `json:"scope"`
	Source       string                 `json:"source"`
	GroupID      string                 `json:"groupId"`
	Owned        bool                   `json:"owned"`
	Available    bool                   `json:"available"`
	AuthorizedAt *time.Time             `json:"authorizedAt,omitempty"`
	ExpiresAt    *time.Time             `json:"expiresAt,omitempty"`
	ExpiryStatus string                 `json:"expiryStatus"`
	CreatedAt    time.Time              `json:"createdAt"`
	Assets       []ArkPortraitAssetView `json:"assets"`
	// 以下字段只在管理员接口返回。
	Owner       *ArkUserSummary  `json:"owner,omitempty"`
	GrantAll    bool             `json:"grantAll"`
	GrantUsers  []ArkUserSummary `json:"grantUsers,omitempty"`
	AssetCounts map[string]int64 `json:"assetCounts,omitempty"`
}

type ArkPortraitGroupRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
}

type ArkPortraitAssetView struct {
	ID             string     `json:"id"`
	GroupID        string     `json:"groupId"`
	ResourceID     string     `json:"resourceId"`
	ArkAssetID     string     `json:"arkAssetId"`
	AssetType      string     `json:"assetType"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Error          string     `json:"error,omitempty"`
	LibraryAssetID string     `json:"libraryAssetId,omitempty"`
	InLibrary      bool       `json:"inLibrary"`
	MimeType       string     `json:"mimeType,omitempty"`
	Width          int        `json:"width,omitempty"`
	Height         int        `json:"height,omitempty"`
	Bytes          int64      `json:"bytes,omitempty"`
	ReviewedAt     *time.Time `json:"reviewedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	// 管理员审核队列附带上传者与所属素材组。
	Uploader *ArkUserSummary      `json:"uploader,omitempty"`
	Group    *ArkPortraitGroupRef `json:"group,omitempty"`
}

type ArkPortraitSessionView struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Scope                string    `json:"scope"`
	Status               string    `json:"status"`
	H5Link               string    `json:"h5Link,omitempty"`
	GroupID              string    `json:"groupId,omitempty"`
	Error                string    `json:"error,omitempty"`
	ReservedMicrocredits int64     `json:"reservedMicrocredits"`
	ExpiresAt            time.Time `json:"expiresAt"`
}

// arkCredentialSetting 读取凭据完整的方舟配置；所有调用方舟控制面的操作都从这里取配置。
func (s *Service) arkCredentialSetting() (arkPrivateAssetSettingValue, error) {
	_, setting, err := s.readArkPrivateAssetSetting()
	if err != nil {
		return setting, err
	}
	if !setting.credentialsReady() {
		return setting, BadAuthRequest("方舟素材库尚未配置完整，请联系管理员填写 Region、ProjectName 与 IAM AK/SK")
	}
	return setting, nil
}

func arkPortraitGroupAvailable(group model.ArkPortraitGroup, setting arkPrivateAssetSettingValue) bool {
	return group.Site == setting.Site && group.Region == setting.Region && group.ProjectName == setting.ProjectName
}

func arkGroupExpired(group model.ArkPortraitGroup, now time.Time) bool {
	return group.ExpiresAt != nil && !group.ExpiresAt.After(now)
}

func arkGroupExpiryStatus(group model.ArkPortraitGroup, now time.Time) string {
	switch {
	case group.ExpiresAt == nil:
		return "none"
	case !group.ExpiresAt.After(now):
		return "expired"
	case group.ExpiresAt.Before(now.Add(arkPortraitExpiringWindow)):
		return "expiring"
	default:
		return "valid"
	}
}

func arkGroupKindLabel(kind string) string {
	switch kind {
	case arkGroupKindVirtual:
		return "虚拟人像"
	case arkGroupKindIP:
		return "版权 IP"
	default:
		return "真人人像"
	}
}

func validArkGroupKind(kind string) bool {
	return kind == arkGroupKindReal || kind == arkGroupKindVirtual || kind == arkGroupKindIP
}

// arkGroupCharge 返回用户自建该类素材组要支付的积分；平台积分功能关闭或未开启收费时为 0。
func (s *Service) arkGroupCharge(setting arkPrivateAssetSettingValue, kind string) (int64, error) {
	price := setting.groupPrice(kind)
	if price <= 0 {
		return 0, nil
	}
	enabled, err := s.FeatureEnabled(FeatureCredits)
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, nil
	}
	return price, nil
}

func (s *Service) arkAssetConfigView(setting arkPrivateAssetSettingValue) (ArkAssetConfigView, error) {
	billing := false
	if setting.BillingEnabled {
		enabled, err := s.FeatureEnabled(FeatureCredits)
		if err != nil {
			return ArkAssetConfigView{}, err
		}
		billing = enabled
	}
	return ArkAssetConfigView{
		RealEnabled: setting.RealEnabled, VirtualEnabled: setting.VirtualEnabled,
		RealGroupLimit: setting.RealGroupLimit, VirtualGroupLimit: setting.VirtualGroupLimit,
		BillingEnabled: billing, RealGroupPriceMicrocredits: setting.RealGroupPriceMicrocredits, VirtualGroupPriceMicrocredits: setting.VirtualGroupPriceMicrocredits,
		RealReviewRequired: setting.RealReviewRequired, VirtualReviewRequired: setting.VirtualReviewRequired,
		RealAuthorizationDays: setting.RealAuthorizationDays,
	}, nil
}

func arkGroupBillingOrder(userID string, key string, kind string, amount int64) *model.BillingOrder {
	return &model.BillingOrder{
		ID: newID(), UserID: userID, IdempotencyKey: "ark-group:" + key,
		Model: "方舟" + strings.ReplaceAll(arkGroupKindLabel(kind), " ", "") + "素材组", Capability: "ark_asset", Scene: model.BillingSceneArkAssetGroup,
		BillingMode: "fixed_request", UnitPriceMicrocredits: amount, MultiplierBasisPoints: 10000, Quantity: 1,
		AmountMicrocredits: amount, ReservedAmountMicrocredits: amount, Status: model.BillingStatusReserved,
	}
}

func arkGroupSettleNote(kind string, name string) string {
	return arkGroupKindLabel(kind) + " · " + name
}

func insufficientArkCredits(amount int64) error {
	return BadAuthRequest(fmt.Sprintf("积分不足：创建该素材组需要 %s 积分，请充值后重试", formatArkCredits(amount)))
}

func formatArkCredits(amount int64) string {
	whole := amount / CreditScale
	fraction := amount % CreditScale
	if fraction == 0 {
		return fmt.Sprintf("%d", whole)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%06d", whole, fraction), "0"), ".")
}

func (s *Service) ArkPortraitOverview(ctx context.Context, actor *model.User) (*ArkPortraitOverview, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	_, setting, err := s.readArkPrivateAssetSetting()
	if err != nil {
		return nil, err
	}
	config, err := s.arkAssetConfigView(setting)
	if err != nil {
		return nil, err
	}
	overview := &ArkPortraitOverview{Config: config, Groups: []ArkPortraitGroupView{}, Sessions: []ArkPortraitSessionView{}}
	if !setting.credentialsReady() {
		return overview, nil
	}
	own, err := s.repo.ArkPortraitUserGroups(actor.ID)
	if err != nil {
		return nil, err
	}
	granted, err := s.repo.ArkPortraitGrantedGroups(actor.ID)
	if err != nil {
		return nil, err
	}
	overview.Enabled = setting.RealEnabled || setting.VirtualEnabled || len(own) > 0 || len(granted) > 0
	if !overview.Enabled {
		return overview, nil
	}
	overview.Site = setting.Site
	now := time.Now()
	views, err := s.arkGroupViewsForUser(actor.ID, own, granted, setting, now)
	if err != nil {
		return nil, err
	}
	overview.Groups = views
	for _, group := range own {
		if group.Kind == arkGroupKindVirtual {
			overview.Usage.Virtual++
		} else if group.Kind == arkGroupKindReal {
			overview.Usage.Real++
		}
	}
	activeSessions, err := s.repo.CountActiveArkPortraitUserSessions(actor.ID, now)
	if err != nil {
		return nil, err
	}
	overview.Usage.Real += activeSessions
	pending, err := s.repo.PendingArkPortraitSessions(actor.ID, arkGroupScopeUser, now, 10)
	if err != nil {
		return nil, err
	}
	overview.Sessions, err = s.arkSessionViews(pending)
	if err != nil {
		return nil, err
	}
	return overview, nil
}

// arkGroupViewsForUser 组装用户可见的素材组：自建组返回全部状态的素材，授权组只返回已通过的素材。
func (s *Service) arkGroupViewsForUser(userID string, own []model.ArkPortraitGroup, granted []model.ArkPortraitGroup, setting arkPrivateAssetSettingValue, now time.Time) ([]ArkPortraitGroupView, error) {
	ownIDs := make([]string, 0, len(own))
	for _, group := range own {
		ownIDs = append(ownIDs, group.ID)
	}
	grantedIDs := make([]string, 0, len(granted))
	for _, group := range granted {
		grantedIDs = append(grantedIDs, group.ID)
	}
	ownAssets, err := s.repo.ArkPortraitAssetsForGroups(ownIDs, nil)
	if err != nil {
		return nil, err
	}
	grantedAssets, err := s.repo.ArkPortraitAssetsForGroups(grantedIDs, []string{arkPortraitAssetActive})
	if err != nil {
		return nil, err
	}
	all := append(append([]model.ArkPortraitAsset{}, ownAssets...), grantedAssets...)
	assetViews, err := s.arkAssetViewsForUser(userID, all)
	if err != nil {
		return nil, err
	}
	byGroup := make(map[string][]ArkPortraitAssetView, len(own)+len(granted))
	for _, view := range assetViews {
		byGroup[view.GroupID] = append(byGroup[view.GroupID], view)
	}
	views := make([]ArkPortraitGroupView, 0, len(own)+len(granted))
	for _, group := range append(append([]model.ArkPortraitGroup{}, own...), granted...) {
		view := arkPortraitGroupView(group, setting, now)
		view.Owned = group.Scope == arkGroupScopeUser && group.UserID == userID
		if items := byGroup[group.ID]; items != nil {
			view.Assets = items
		}
		views = append(views, view)
	}
	return views, nil
}

// arkAssetViewsForUser 附带资源尺寸，并标记各素材是否已在当前用户的素材库中。
func (s *Service) arkAssetViewsForUser(userID string, assets []model.ArkPortraitAsset) ([]ArkPortraitAssetView, error) {
	resourceIDs := make([]string, 0, len(assets))
	libraryIDs := make([]string, 0, len(assets))
	for _, asset := range assets {
		resourceIDs = append(resourceIDs, asset.ResourceID)
		if id := arkLibraryAssetIDForUser(asset, userID); id != "" {
			libraryIDs = append(libraryIDs, id)
		}
	}
	resources, err := s.repo.ResourcesByIDs(resourceIDs)
	if err != nil {
		return nil, err
	}
	resourceByID := make(map[string]model.Resource, len(resources))
	for _, resource := range resources {
		resourceByID[resource.ID] = resource
	}
	libraryAssets, err := s.repo.AssetsForUserIDs(userID, libraryIDs)
	if err != nil {
		return nil, err
	}
	inLibrary := make(map[string]bool, len(libraryAssets))
	for _, asset := range libraryAssets {
		if asset.Status != model.AssetVersionStatusArchived {
			inLibrary[asset.ID] = true
		}
	}
	views := make([]ArkPortraitAssetView, 0, len(assets))
	for _, asset := range assets {
		view := arkPortraitAssetView(asset)
		if resource, ok := resourceByID[asset.ResourceID]; ok {
			view.MimeType, view.Width, view.Height, view.Bytes = resource.MimeType, resource.Width, resource.Height, resource.Size
		}
		libraryID := arkLibraryAssetIDForUser(asset, userID)
		if inLibrary[libraryID] {
			view.LibraryAssetID = libraryID
			view.InLibrary = true
		} else if asset.UserID == userID {
			view.LibraryAssetID = asset.LibraryAssetID
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) arkSessionViews(sessions []model.ArkPortraitSession) ([]ArkPortraitSessionView, error) {
	orderIDs := make([]string, 0, len(sessions))
	for _, session := range sessions {
		if session.BillingOrderID != "" {
			orderIDs = append(orderIDs, session.BillingOrderID)
		}
	}
	orders, err := s.repo.BillingOrdersByIDs(orderIDs)
	if err != nil {
		return nil, err
	}
	views := make([]ArkPortraitSessionView, 0, len(sessions))
	for _, session := range sessions {
		view := arkPortraitSessionView(session)
		if order, ok := orders[session.BillingOrderID]; ok && order.Status == model.BillingStatusReserved {
			view.ReservedMicrocredits = order.AmountMicrocredits
		}
		views = append(views, view)
	}
	return views, nil
}

// CreateArkPortraitSession 为用户自建真人素材组生成 H5 认证链接。lang 为界面语言，映射到 H5 支持的 zh / en / zh-Hant。
func (s *Service) validateArkPortraitReferences(userID string, input *canvasGenerationInput) error {
	if input == nil {
		return nil
	}
	ids := make([]string, 0)
	for _, group := range [][]providerMedia{input.ReferenceImages, input.ReferenceVideos, input.ReferenceAudios} {
		for _, reference := range group {
			if id, ok := strings.CutPrefix(strings.TrimSpace(reference.URL), "asset://"); ok && strings.TrimSpace(id) != "" {
				ids = append(ids, strings.TrimSpace(id))
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.repo.ArkPortraitAssetAccessRows(ids)
	if err != nil || len(rows) == 0 {
		return err
	}
	platformGroupIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Scope == arkGroupScopePlatform && !row.GrantAll && row.GroupUserID != userID {
			platformGroupIDs = append(platformGroupIDs, row.GroupID)
		}
	}
	granted, err := s.repo.ArkPortraitGrantedGroupIDs(userID, platformGroupIDs)
	if err != nil {
		return err
	}
	rowsByAsset := make(map[string][]repository.ArkPortraitAssetAccess, len(rows))
	for _, row := range rows {
		rowsByAsset[row.ArkAssetID] = append(rowsByAsset[row.ArkAssetID], row)
	}
	now := time.Now()
	for _, id := range ids {
		candidates := rowsByAsset[id]
		if len(candidates) == 0 {
			continue
		}
		if err := validateArkAssetCandidates(userID, candidates, granted, now); err != nil {
			return err
		}
	}
	return nil
}

func arkPortraitAssetType(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "Image"
	case strings.HasPrefix(mimeType, "video/"):
		return "Video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "Audio"
	default:
		return ""
	}
}

func arkPortraitNotFound(err error) bool {
	message := err.Error()
	return strings.Contains(message, "NotFound") || strings.Contains(message, "not found")
}

// safeArkPortraitError 只保留方舟返回的错误码与说明，去掉可能包含签名地址的长文本。
func safeArkPortraitError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if index := strings.Index(message, "http"); index >= 0 && strings.Contains(message[index:], "?") {
		message = message[:index]
	}
	return truncateRunes(strings.TrimSpace(message), 300)
}

func arkPortraitGroupView(group model.ArkPortraitGroup, setting arkPrivateAssetSettingValue, now time.Time) ArkPortraitGroupView {
	return ArkPortraitGroupView{
		ID: group.ID, Name: group.Name, Description: group.Description, Kind: firstNonEmpty(group.Kind, arkGroupKindReal),
		Scope: firstNonEmpty(group.Scope, arkGroupScopeUser), Source: group.Source, GroupID: group.GroupID,
		Available: arkPortraitGroupAvailable(group, setting), AuthorizedAt: group.AuthorizedAt, ExpiresAt: group.ExpiresAt,
		ExpiryStatus: arkGroupExpiryStatus(group, now), CreatedAt: group.CreatedAt, GrantAll: group.GrantAll,
		Assets: []ArkPortraitAssetView{},
	}
}

func arkPortraitAssetView(asset model.ArkPortraitAsset) ArkPortraitAssetView {
	return ArkPortraitAssetView{
		ID: asset.ID, GroupID: asset.GroupID, ResourceID: asset.ResourceID, ArkAssetID: asset.ArkAssetID,
		AssetType: asset.AssetType, Name: asset.Name, Status: asset.Status, Error: asset.Error,
		ReviewedAt: asset.ReviewedAt, CreatedAt: asset.CreatedAt,
	}
}

func arkPortraitSessionView(session model.ArkPortraitSession) ArkPortraitSessionView {
	return ArkPortraitSessionView{
		ID: session.ID, Name: session.Name, Scope: firstNonEmpty(session.Scope, arkGroupScopeUser), Status: session.Status,
		GroupID: session.GroupID, Error: session.Error, ExpiresAt: session.ExpiresAt,
	}
}

func validateArkAssetCandidates(userID string, candidates []repository.ArkPortraitAssetAccess, granted map[string]bool, now time.Time) error {
	allowed, expired := false, false
	for _, row := range candidates {
		permitted := (row.Scope != arkGroupScopePlatform && row.GroupUserID == userID) ||
			(row.Scope == arkGroupScopePlatform && (row.GrantAll || row.GroupUserID == userID || granted[row.GroupID]))
		if !permitted {
			continue
		}
		if row.ExpiresAt != nil && !row.ExpiresAt.After(now) {
			expired = true
			continue
		}
		allowed = true
	}
	if allowed {
		return nil
	}
	if expired {
		return Forbidden("参考素材所在的真人素材组授权已过期，请重新认证后再生成")
	}
	return Forbidden("参考素材包含未授权给你的方舟素材，不能用于生成")
}
