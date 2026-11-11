package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) CreateArkPortraitSession(ctx context.Context, actor *model.User, name, lang string) (*ArkPortraitSessionView, error) {
	return s.createArkPortraitSession(ctx, actor, name, "", lang, arkGroupScopeUser)
}

func (s *Service) createArkPortraitSession(ctx context.Context, actor *model.User, name, description, lang, scope string) (*ArkPortraitSessionView, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if err := validateArkSessionDetails(name, description); err != nil {
		return nil, err
	}
	setting, err := s.arkCredentialSetting()
	if err != nil {
		return nil, err
	}
	if scope == arkGroupScopeUser && !setting.RealEnabled {
		return nil, Forbidden("管理员尚未开放真人素材组")
	}
	callbackBase, err := arkPortraitCallbackBase(setting)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	recent, err := s.repo.CountRecentArkPortraitSessions(actor.ID, now.Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	if recent >= maxArkPortraitSessionsPerHour {
		return nil, RateLimited("真人认证发起过于频繁，请一小时后再试")
	}
	var order *model.BillingOrder
	session := &model.ArkPortraitSession{
		ID: newID(), UserID: actor.ID, Scope: scope, State: randomToken(), Name: name, Description: description,
		Site: setting.Site, Region: setting.Region, ProjectName: setting.ProjectName,
		Status: arkPortraitSessionPending, ExpiresAt: now.Add(arkPortraitSessionTTL),
	}
	if !arkPortraitStatePattern.MatchString(session.State) {
		return nil, errors.New("生成真人认证凭据失败")
	}
	order, err = s.prepareArkSessionCharge(setting, session)
	if err != nil {
		return nil, err
	}
	// 先登记会话并冻结费用，再调用方舟；方舟失败时退回冻结积分并把会话标记失败。
	if err := s.repo.CreateArkPortraitSessionWithReservation(session, order); err != nil {
		if errors.Is(err, repository.ErrInsufficientCredits) && order != nil {
			return nil, insufficientArkCredits(order.AmountMicrocredits)
		}
		return nil, err
	}
	fail := func(message string, cause error) (*ArkPortraitSessionView, error) {
		if _, finishErr := s.repo.FinishArkPortraitSession(session.ID, arkPortraitSessionFailed, "", message, "发起真人认证失败，冻结积分已退回"); finishErr != nil {
			return nil, errors.Join(cause, finishErr)
		}
		return nil, WrapAppError(http.StatusBadGateway, message, cause)
	}
	response, err := callArkPrivateAssetAPI(ctx, setting, "CreateVisualValidateSession", map[string]interface{}{
		"CallbackURL": callbackBase + "/api/ark-portraits/callback/" + session.State,
		"ProjectName": setting.ProjectName,
	})
	if err != nil {
		return fail("发起方舟真人认证失败："+safeArkPortraitError(err), err)
	}
	bytedToken := arkPrivateAssetResponseField(response, "BytedToken")
	h5Link, linkErr := arkPortraitH5Link(arkPrivateAssetResponseField(response, "H5Link"), lang)
	if bytedToken == "" || linkErr != nil {
		return fail("方舟未返回有效的真人认证链接", errors.New("invalid visual validate session"))
	}
	tokenCipher, err := s.encryptSettingSecret(bytedToken)
	if err != nil {
		return fail("保存真人认证凭据失败", err)
	}
	if err := s.repo.SetArkPortraitSessionToken(session.ID, tokenCipher); err != nil {
		return fail("保存真人认证凭据失败", err)
	}
	view := arkPortraitSessionView(*session)
	if order != nil {
		view.ReservedMicrocredits = order.AmountMicrocredits
	}
	// H5 链接内含方舟签发的临时凭据，只在发起时返回给本人，不落库。
	view.H5Link = h5Link
	return &view, nil
}

// HandleArkPortraitCallback 处理 H5 认证页完成后的跳转（无登录态，多在手机上打开），返回结果页地址。
func (s *Service) HandleArkPortraitCallback(ctx context.Context, state string, query url.Values) string {
	result := func(status string, scope string) string {
		target := arkPortraitResultPath + "?status=" + url.QueryEscape(status)
		if scope == arkGroupScopePlatform {
			target += "&scope=platform"
		}
		return target
	}
	state = strings.TrimSpace(state)
	if !arkPortraitStatePattern.MatchString(state) {
		return result("invalid", "")
	}
	session, err := s.repo.ArkPortraitSessionByState(state)
	if err != nil {
		return result("invalid", "")
	}
	if session.Status != arkPortraitSessionPending {
		return result(session.Status, session.Scope)
	}
	if !session.ExpiresAt.After(time.Now()) {
		s.expireArkPortraitSession(session.ID)
		return result(arkPortraitSessionExpired, session.Scope)
	}
	resultCode := strings.TrimSpace(query.Get("resultCode"))
	if resultCode != arkPortraitSuccessCode {
		detail := "真人认证未通过（resultCode=" + truncateRunes(resultCode, 20)
		if sub := strings.TrimSpace(query.Get("algorithmBaseRespCode")); sub != "" {
			detail += "，algorithmBaseRespCode=" + truncateRunes(sub, 20)
		}
		_, _ = s.repo.FinishArkPortraitSession(session.ID, arkPortraitSessionFailed, truncateRunes(resultCode, 40), detail+"），请重新发起认证", "真人认证未通过，冻结积分已退回")
		return result(arkPortraitSessionFailed, session.Scope)
	}
	if token := strings.TrimSpace(query.Get("bytedToken")); token != "" {
		stored, err := s.decryptSettingSecret(session.BytedTokenCipher)
		if err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(token)) != 1 {
			return result("invalid", "")
		}
	}
	if _, err := s.completeArkPortraitSession(ctx, session); err != nil {
		// 保持 pending，用户回到电脑端可点击“我已完成认证”重试换取素材组。
		return result(arkPortraitSessionPending, session.Scope)
	}
	return result(arkPortraitSessionVerified, session.Scope)
}

// ArkPortraitSessionStatus 只读取本地会话状态，供电脑端轮询回调结果；不调用方舟。
func (s *Service) ArkPortraitSessionStatus(actor *model.User, id string) (*ArkPortraitSessionView, error) {
	session, err := s.arkPortraitSessionForUser(actor, id)
	if err != nil {
		return nil, err
	}
	if session.Status == arkPortraitSessionPending && !session.ExpiresAt.After(time.Now()) {
		s.expireArkPortraitSession(session.ID)
		if session, err = s.repo.ArkPortraitSessionForUser(actor.ID, session.ID); err != nil {
			return nil, err
		}
	}
	views, err := s.arkSessionViews([]model.ArkPortraitSession{*session})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// CheckArkPortraitSession 是回调未到达（例如在手机上完成认证）时的主动查询入口。
func (s *Service) CheckArkPortraitSession(ctx context.Context, actor *model.User, id string) (*ArkPortraitSessionView, error) {
	session, err := s.arkPortraitSessionForUser(actor, id)
	if err != nil {
		return nil, err
	}
	if session.Status == arkPortraitSessionPending && !session.ExpiresAt.After(time.Now()) {
		s.expireArkPortraitSession(session.ID)
	} else if session.Status == arkPortraitSessionPending {
		if _, err := s.completeArkPortraitSession(ctx, session); err != nil {
			return nil, err
		}
	}
	return s.ArkPortraitSessionStatus(actor, session.ID)
}

// CancelArkPortraitSession 结束仍在等待的认证会话并退回冻结的素材组费用。
func (s *Service) CancelArkPortraitSession(actor *model.User, id string) (*ArkPortraitSessionView, error) {
	session, err := s.arkPortraitSessionForUser(actor, id)
	if err != nil {
		return nil, err
	}
	if session.Status == arkPortraitSessionPending {
		if _, err := s.repo.FinishArkPortraitSession(session.ID, arkPortraitSessionCancelled, "", "已取消真人认证", "已取消真人认证，冻结积分已退回"); err != nil {
			return nil, err
		}
	}
	return s.ArkPortraitSessionStatus(actor, session.ID)
}

func (s *Service) arkPortraitSessionForUser(actor *model.User, id string) (*model.ArkPortraitSession, error) {
	if actor == nil {
		return nil, Unauthorized("请先登录")
	}
	session, err := s.repo.ArkPortraitSessionForUser(actor.ID, strings.TrimSpace(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("认证会话不存在")
	}
	return session, err
}

func (s *Service) expireArkPortraitSession(id string) {
	_, _ = s.repo.FinishArkPortraitSession(id, arkPortraitSessionExpired, "", "认证会话已过期", "真人认证未在有效期内完成，冻结积分已退回")
}

func (s *Service) completeArkPortraitSession(ctx context.Context, session *model.ArkPortraitSession) (*model.ArkPortraitGroup, error) {
	setting, err := s.arkCredentialSetting()
	if err != nil {
		return nil, err
	}
	if session.Site != setting.Site || session.Region != setting.Region || session.ProjectName != setting.ProjectName {
		return nil, NewAppError(http.StatusConflict, "管理员已更换方舟站点、Region 或项目，请重新发起真人认证")
	}
	token, err := s.decryptSettingSecret(session.BytedTokenCipher)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, NewAppError(http.StatusConflict, "认证会话尚未就绪，请重新发起真人认证")
	}
	response, err := callArkPrivateAssetAPI(ctx, setting, "GetVisualValidateResult", map[string]interface{}{
		"BytedToken":  token,
		"ProjectName": session.ProjectName,
	})
	if err != nil {
		return nil, NewAppError(http.StatusConflict, "尚未获取到认证结果："+safeArkPortraitError(err)+"。请在认证页点击“完成”后再试")
	}
	groupID := arkPrivateAssetResponseField(response, "GroupId", "GroupID", "group_id")
	if !arkPortraitGroupIDPattern.MatchString(groupID) {
		return nil, NewAppError(http.StatusBadGateway, "方舟未返回有效的真人素材组 ID")
	}
	now := time.Now()
	expiresAt := now.Add(time.Duration(setting.RealAuthorizationDays) * 24 * time.Hour)
	group := &model.ArkPortraitGroup{
		ID: newID(), UserID: session.UserID, Kind: arkGroupKindReal, Scope: firstNonEmpty(session.Scope, arkGroupScopeUser),
		Name: session.Name, Description: session.Description, Site: setting.Site, Region: setting.Region, ProjectName: setting.ProjectName,
		GroupID: groupID, Source: arkGroupSourceLiveness, AuthorizedAt: &now, ExpiresAt: &expiresAt, BillingOrderID: session.BillingOrderID,
	}
	registered, err := s.repo.CompleteArkPortraitSession(session.ID, group, arkGroupSettleNote(arkGroupKindReal, session.Name))
	if errors.Is(err, repository.ErrArkPortraitGroupTaken) {
		_, _ = s.repo.FinishArkPortraitSession(session.ID, arkPortraitSessionFailed, arkPortraitSuccessCode, "该真人素材组已被其他账号登记", "真人素材组已被其他账号登记，冻结积分已退回")
		return nil, NewAppError(http.StatusConflict, "该真人素材组已被其他账号登记")
	}
	if errors.Is(err, repository.ErrArkPortraitSessionClosed) {
		return nil, NewAppError(http.StatusConflict, "认证会话已结束，请重新发起真人认证")
	}
	return registered, err
}

// validateArkPortraitReferences 校验生成任务引用的方舟素材 ID：素材组都在平台的同一方舟账号下，
// 仅靠方舟侧无法区分本系统内的用户。他人个人组、未授权的平台组和已过期的真人组都拒绝；
// 未登记的素材 ID（方舟公共虚拟人像或其他来源）不受限制。
func arkPortraitCallbackBase(setting arkPrivateAssetSettingValue) (string, error) {
	base := strings.TrimRight(firstNonEmpty(setting.CallbackBaseURL, strings.TrimSpace(os.Getenv("CANVAS_PUBLIC_BASE_URL"))), "/")
	if base == "" {
		return "", BadAuthRequest("管理员尚未配置真人认证回调地址（方舟素材库设置或 CANVAS_PUBLIC_BASE_URL）")
	}
	if err := validatePaymentPublicBaseURL(base); err != nil {
		return "", BadAuthRequest("真人认证回调地址必须是有效的 HTTP(S) 根地址")
	}
	return base, nil
}

// arkPortraitH5Link 校验方舟返回的认证页并按界面语言设置 lng（H5 只支持 zh / en / zh-Hant）。
func arkPortraitH5Link(raw, lang string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("invalid h5 link")
	}
	query := parsed.Query()
	query.Set("lng", arkPortraitH5Language(lang))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func arkPortraitH5Language(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch {
	case lang == "zh-hant" || lang == "zh-tw" || lang == "zh-hk":
		return "zh-Hant"
	case lang == "" || strings.HasPrefix(lang, "zh"):
		return "zh"
	default:
		return "en"
	}
}

func (s *Service) prepareArkSessionCharge(setting arkPrivateAssetSettingValue, session *model.ArkPortraitSession) (*model.BillingOrder, error) {
	if session.Scope != arkGroupScopeUser {
		return nil, nil
	}
	groups, err := s.repo.CountArkPortraitUserGroups(session.UserID, arkGroupKindReal)
	if err != nil {
		return nil, err
	}
	sessions, err := s.repo.CountActiveArkPortraitUserSessions(session.UserID, time.Now())
	if err != nil {
		return nil, err
	}
	if groups+sessions >= int64(setting.RealGroupLimit) {
		return nil, BadAuthRequest(fmt.Sprintf("每个账号最多创建 %d 个真人素材组（含认证中的会话）", setting.RealGroupLimit))
	}
	price, err := s.arkGroupCharge(setting, arkGroupKindReal)
	if err != nil {
		return nil, err
	}
	if price > 0 {
		order := arkGroupBillingOrder(session.UserID, "session:"+session.ID, arkGroupKindReal, price)
		session.BillingOrderID = order.ID
		return order, nil
	}
	return nil, nil
}

func validateArkSessionDetails(name, description string) error {
	if name == "" || len([]rune(name)) > 40 {
		return BadAuthRequest("请填写 1-40 个字的真人名称，便于区分不同素材组")
	}
	if len([]rune(description)) > 200 {
		return BadAuthRequest("素材组说明最多 200 个字")
	}
	return nil
}
