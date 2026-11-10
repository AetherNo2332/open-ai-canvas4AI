package database

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

// Append upstream migrations after the fork platform migrations without reusing applied versions.
func init() { schemaMigrations = append(schemaMigrations, upstreamNovemberMigrations...) }

var upstreamNovemberMigrations = []migration{
	{version: 86, name: "drama_cast_voice_looks", checksum: "sha256:drama-cast-voice-looks-v78-20261101", apply: migrateDramaCastVoiceLooks},
	{version: 87, name: "ecommerce_agent_and_knowledge", checksum: "sha256:ecommerce-agent-and-knowledge-v79-20261101", apply: migrateEcommerceFeature},
	{version: 88, name: "payment_order_charge_method", checksum: "sha256:payment-order-charge-method-v80-20261104", apply: migratePaymentOrderChargeMethod},
	{version: 89, name: "ark_portrait_assets", checksum: "sha256:ark-portrait-assets-v81-20261104", apply: func(tx *gorm.DB) error {
		// 方舟真人认证会话、用户真人素材组与组内素材；只建新表，不改写可信素材绑定。
		return tx.AutoMigrate(&model.ArkPortraitSession{}, &model.ArkPortraitGroup{}, &model.ArkPortraitAsset{})
	}},
	{version: 90, name: "token_cache_write_price", checksum: "sha256:token-cache-write-price-v82-20261105", apply: migrateTokenCacheWritePrice},
	{version: 91, name: "skill_admin_edits", checksum: "sha256:skill-admin-edits-v83-20261105", apply: migrateSkillAdminEdits},
	{version: 92, name: "token_price_rules", checksum: "sha256:token-price-rules-v84-20261105", apply: migrateTokenPriceRules},
	{version: 93, name: "pose_library", checksum: "sha256:pose-library-v85-20261105", apply: func(tx *gorm.DB) error {
		// 站点级姿势库：来源署名与参考图条目，只建新表；图片文件在数据目录 pose-library/ 下。
		return tx.AutoMigrate(&model.PoseLibrarySource{}, &model.PoseLibraryItem{})
	}},
	{version: 94, name: "ark_asset_groups", checksum: "sha256:ark-asset-groups-v86-20261105", apply: migrateArkAssetGroups},
	{version: 95, name: "inspiration_feed", checksum: "sha256:inspiration-feed-v87-20261105", apply: func(tx *gorm.DB) error {
		// 灵感上新：定时同步的上游灵感条目，只建新表；开关与同步记录存 system_settings。
		return tx.AutoMigrate(&model.InspirationFeedEntry{})
	}},
	{version: 96, name: "reference_video_billing", checksum: "sha256:reference-video-billing-v88-20261106", apply: migrateReferenceVideoBilling},
	{version: 97, name: "reference_video_cost_price", checksum: "sha256:reference-video-cost-price-v89-20261106", apply: migrateReferenceVideoCostPrice},
	{version: 98, name: "drama_draft_form", checksum: "sha256:drama-draft-form-v90-20261106", apply: func(tx *gorm.DB) error {
		// 草稿的创建页表单；旧作品为空串，草稿只能在制作台开工，不能回到创建页。
		if !tx.Migrator().HasTable(&model.DramaProduction{}) || tx.Migrator().HasColumn(&model.DramaProduction{}, "DraftJSON") {
			return nil
		}
		return tx.Migrator().AddColumn(&model.DramaProduction{}, "DraftJSON")
	}},
	{version: 99, name: "email_logs_and_notifications", checksum: "sha256:email-logs-and-notifications-v91-20261106", apply: func(tx *gorm.DB) error {
		// 邮件发送记录与用户邮件订阅（消费日报，默认关闭）；只建新表。
		return tx.AutoMigrate(&model.EmailLog{}, &model.UserNotificationSetting{})
	}},
	{version: 100, name: "drama_reference_controls", checksum: "sha256:drama-reference-controls-v92", apply: func(tx *gorm.DB) error {
		for _, change := range []struct {
			entity any
			fields []string
		}{
			{&model.DramaCast{}, []string{"ImagePrompt"}},
			{&model.DramaSegment{}, []string{"ReferenceImagesJSON", "AudioCastIDsJSON"}},
		} {
			if !tx.Migrator().HasTable(change.entity) {
				continue
			}
			for _, field := range change.fields {
				if !tx.Migrator().HasColumn(change.entity, field) {
					if err := tx.Migrator().AddColumn(change.entity, field); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}},
}

// migrateReferenceVideoCostPrice 为价格档成本和账单成本快照增加带参考视频的成本价。
// 新列 NOT NULL DEFAULT 0，旧订单的成本口径不变。版本 88 自动开启参考视频计费的全 Token 视频模型，
// 挂参考视频的订单从此按带参考视频成本价记成本：这些 Token 档把带参考视频成本价回填为现有成本价，
// 否则升级后这类订单的成本会记成 0，直到管理员重新保存模型。
func migrateReferenceVideoCostPrice(tx *gorm.DB) error {
	columns := []string{"cost_reference_video_unit_price_microcredits", "cost_reference_video_output_token_price_microcredits"}
	for _, entity := range []any{&model.ChannelModelPriceTier{}, &model.BillingOrder{}} {
		if !tx.Migrator().HasTable(entity) {
			continue
		}
		for _, column := range columns {
			if tx.Migrator().HasColumn(entity, column) {
				continue
			}
			if err := tx.Migrator().AddColumn(entity, column); err != nil {
				return err
			}
		}
	}
	if !tx.Migrator().HasTable(&model.ChannelModel{}) || !tx.Migrator().HasTable(&model.ChannelModelPriceTier{}) {
		return nil
	}
	enabled := tx.Model(&model.ChannelModel{}).Select("id").Where("capability = ? AND reference_video_billing = ?", "video", true)
	return tx.Model(&model.ChannelModelPriceTier{}).
		Where("billing_mode = ? AND channel_model_id IN (?) AND cost_reference_video_output_token_price_microcredits = ?", "token", enabled, 0).
		Update("cost_reference_video_output_token_price_microcredits", gorm.Expr("cost_output_token_price_microcredits")).Error
}

// migrateReferenceVideoBilling 增加视频模型的“参考视频计费”开关、价格档的带参考视频价格和账单的参考视频快照。
// 开关默认关闭；只有已经全部按 Token 计费的视频模型开启开关，并把带参考视频的 Token 价格填成现价，
// 这些模型原本就把参考视频计入公式用量，迁移后扣费结果不变。含按秒价格档的模型保持关闭，
// 避免按秒订单因计入参考视频秒数而悄悄涨价。
func migrateReferenceVideoBilling(tx *gorm.DB) error {
	for _, target := range []struct {
		entity  any
		columns []string
	}{
		{&model.ChannelModel{}, []string{"reference_video_billing"}},
		{&model.ChannelModelPriceTier{}, []string{"reference_video_unit_price_microcredits", "reference_video_output_token_price_microcredits"}},
		{&model.BillingOrder{}, []string{"reference_video_count", "reference_video_millis", "reference_video_billed"}},
	} {
		if !tx.Migrator().HasTable(target.entity) {
			continue
		}
		for _, column := range target.columns {
			if tx.Migrator().HasColumn(target.entity, column) {
				continue
			}
			if err := tx.Migrator().AddColumn(target.entity, column); err != nil {
				return err
			}
		}
	}
	if !tx.Migrator().HasTable(&model.ChannelModel{}) || !tx.Migrator().HasTable(&model.ChannelModelPriceTier{}) {
		return nil
	}
	var tokenModelIDs, perSecondModelIDs []string
	if err := tx.Model(&model.ChannelModelPriceTier{}).Where("billing_mode = ?", "token").Distinct().Pluck("channel_model_id", &tokenModelIDs).Error; err != nil {
		return err
	}
	if err := tx.Model(&model.ChannelModelPriceTier{}).Where("billing_mode = ?", "per_second").Distinct().Pluck("channel_model_id", &perSecondModelIDs).Error; err != nil {
		return err
	}
	excluded := make(map[string]bool, len(perSecondModelIDs))
	for _, id := range perSecondModelIDs {
		excluded[id] = true
	}
	candidates := make([]string, 0, len(tokenModelIDs))
	for _, id := range tokenModelIDs {
		if !excluded[id] {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	var videoModelIDs []string
	if err := tx.Model(&model.ChannelModel{}).Where("capability = ? AND id IN ?", "video", candidates).Pluck("id", &videoModelIDs).Error; err != nil {
		return err
	}
	if len(videoModelIDs) == 0 {
		return nil
	}
	if err := tx.Model(&model.ChannelModel{}).Where("id IN ?", videoModelIDs).Update("reference_video_billing", true).Error; err != nil {
		return err
	}
	return tx.Model(&model.ChannelModelPriceTier{}).Where("billing_mode = ? AND channel_model_id IN ?", "token", videoModelIDs).
		Update("reference_video_output_token_price_microcredits", gorm.Expr("output_token_price_microcredits")).Error
}

// migrateArkAssetGroups 把方舟真人素材库扩展为真人 / 虚拟 / 版权 IP 三类素材组：
//   - 会话、素材组、素材补列；旧素材组均为用户自建的真人组，旧素材无需审核。
//   - 新建平台素材组授权表。
//   - 设置 JSON 的 portraitEnabled 改名为 realEnabled，保留管理员已开放的状态。
//   - 由旧版「加入素材库」写入的真人素材（metadata.source=ark-portrait）转为方舟认证分类，
//     并删除已废弃的手动 portraitCertified 标记。
func migrateArkAssetGroups(tx *gorm.DB) error {
	for _, target := range []struct {
		entity  any
		columns []string
	}{
		{&model.ArkPortraitSession{}, []string{"scope", "description", "billing_order_id"}},
		{&model.ArkPortraitGroup{}, []string{"kind", "scope", "description", "grant_all", "authorized_at", "expires_at", "billing_order_id"}},
		{&model.ArkPortraitAsset{}, []string{"library_asset_id", "reviewed_by", "reviewed_at"}},
	} {
		if !tx.Migrator().HasTable(target.entity) {
			continue
		}
		for _, column := range target.columns {
			if tx.Migrator().HasColumn(target.entity, column) {
				continue
			}
			if err := tx.Migrator().AddColumn(target.entity, column); err != nil {
				return fmt.Errorf("补充方舟素材列 %s：%w", column, err)
			}
		}
	}
	for _, index := range []struct {
		entity any
		field  string
	}{
		{&model.ArkPortraitGroup{}, "Kind"},
		{&model.ArkPortraitGroup{}, "Scope"},
		{&model.ArkPortraitAsset{}, "LibraryAssetID"},
	} {
		if !tx.Migrator().HasTable(index.entity) || tx.Migrator().HasIndex(index.entity, index.field) {
			continue
		}
		if err := tx.Migrator().CreateIndex(index.entity, index.field); err != nil {
			return fmt.Errorf("创建方舟素材索引 %s：%w", index.field, err)
		}
	}
	if err := tx.AutoMigrate(&model.ArkPortraitGroupGrant{}); err != nil {
		return fmt.Errorf("创建方舟素材组授权表：%w", err)
	}
	if err := migrateArkPortraitSettingKey(tx); err != nil {
		return err
	}
	return migrateArkPortraitLibraryAssets(tx)
}

func migrateArkPortraitSettingKey(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&model.SystemSetting{}) {
		return nil
	}
	var setting model.SystemSetting
	err := tx.Where("key = ?", "ark_private_assets").Limit(1).Find(&setting).Error
	if err != nil {
		return fmt.Errorf("读取方舟素材库配置：%w", err)
	}
	if setting.Key == "" || strings.TrimSpace(setting.ValueJSON) == "" {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(setting.ValueJSON), &value); err != nil {
		return fmt.Errorf("解析方舟素材库配置：%w", err)
	}
	legacy, ok := value["portraitEnabled"]
	if !ok {
		return nil
	}
	if _, exists := value["realEnabled"]; !exists {
		value["realEnabled"] = legacy
	}
	delete(value, "portraitEnabled")
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return tx.Model(&model.SystemSetting{}).Where("key = ?", setting.Key).Update("value_json", string(encoded)).Error
}

func migrateArkPortraitLibraryAssets(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&model.Asset{}) {
		return nil
	}
	var assets []model.Asset
	if err := tx.Where("payload_json LIKE ?", `%portraitCertified%`).Find(&assets).Error; err != nil {
		return fmt.Errorf("读取旧人像认证素材：%w", err)
	}
	now := time.Now().UTC()
	for _, asset := range assets {
		var payload map[string]any
		if err := json.Unmarshal([]byte(asset.PayloadJSON), &payload); err != nil {
			// 无法解析的历史记录保持原样，交给现有坏数据隔离逻辑。
			continue
		}
		if _, ok := payload["portraitCertified"]; !ok {
			continue
		}
		delete(payload, "portraitCertified")
		category := asset.Category
		metadata, _ := payload["metadata"].(map[string]any)
		arkAssetID, _ := payload["arkAssetId"].(string)
		if source, _ := metadata["source"].(string); source == "ark-portrait" && strings.TrimSpace(arkAssetID) != "" && asset.Kind == "image" {
			certifiedAt := asset.UpdatedAt
			if certifiedAt.IsZero() {
				certifiedAt = now
			}
			certification := map[string]any{"kind": "real", "scope": "user", "certifiedAt": certifiedAt.UTC().Format(time.RFC3339Nano), "previousCategory": string(asset.Category)}
			if arkGroupID, _ := metadata["arkPortraitGroupId"].(string); arkGroupID != "" {
				certification["arkGroupId"] = arkGroupID
			}
			var portrait model.ArkPortraitAsset
			if tx.Migrator().HasTable(&model.ArkPortraitAsset{}) {
				if err := tx.Where("user_id = ? AND ark_asset_id = ?", asset.UserID, strings.TrimSpace(arkAssetID)).Limit(1).Find(&portrait).Error; err != nil {
					return fmt.Errorf("读取方舟真人素材：%w", err)
				}
			}
			if portrait.ID != "" {
				certification["portraitAssetId"] = portrait.ID
				certification["groupId"] = portrait.GroupID
				if portrait.LibraryAssetID == "" {
					if err := tx.Model(&model.ArkPortraitAsset{}).Where("id = ?", portrait.ID).Update("library_asset_id", asset.ID).Error; err != nil {
						return fmt.Errorf("关联方舟真人素材：%w", err)
					}
				}
			}
			payload["arkCertification"] = certification
			payload["category"] = string(model.AssetCategoryArk)
			category = model.AssetCategoryArk
		}
		payload["updatedAt"] = now.Format(time.RFC3339Nano)
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.Asset{}).Where("id = ? AND user_id = ?", asset.ID, asset.UserID).Updates(map[string]any{"payload_json": string(encoded), "category": category, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("更新旧人像认证素材：%w", err)
		}
	}
	return nil
}

// migrateTokenPriceRules 为 Token 价格档增加分时与上下文规则，为订单增加规则快照与命中序号。
// 旧价格档规则为空且关闭，旧订单快照为空串，结算仍只用基础 Token 单价。
func migrateTokenPriceRules(tx *gorm.DB) error {
	for _, target := range []struct {
		entity  any
		columns []string
	}{
		{&model.ChannelModelPriceTier{}, []string{"token_price_rules_enabled", "token_price_rules"}},
		{&model.BillingOrder{}, []string{"token_price_rules_json", "token_price_rule"}},
	} {
		if !tx.Migrator().HasTable(target.entity) {
			continue
		}
		for _, column := range target.columns {
			if tx.Migrator().HasColumn(target.entity, column) {
				continue
			}
			if err := tx.Migrator().AddColumn(target.entity, column); err != nil {
				return err
			}
		}
	}
	return nil
}

// migrateSkillAdminEdits 为技能增加管理员修改时间。旧技能两列为空，内置技能启动同步和 GitHub 同步行为不变。
func migrateSkillAdminEdits(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&model.Skill{}) {
		return nil
	}
	for _, column := range []string{"admin_info_edited_at", "admin_content_edited_at"} {
		if tx.Migrator().HasColumn(&model.Skill{}, column) {
			continue
		}
		if err := tx.Migrator().AddColumn(&model.Skill{}, column); err != nil {
			return err
		}
	}
	return nil
}

// migrateTokenCacheWritePrice 为文本 Token 定价增加缓存写入单价，为调用日志和账单增加缓存写入用量。
// 新列都是 NOT NULL DEFAULT 0：旧价格档和旧账单的缓存写入不单独收费，结算结果不变。
func migrateTokenCacheWritePrice(tx *gorm.DB) error {
	for _, target := range []struct {
		entity  any
		columns []string
	}{
		{&model.ChannelModel{}, []string{"cache_write_token_price_microcredits"}},
		{&model.ChannelModelPriceTier{}, []string{"cache_write_token_price_microcredits", "cost_cache_write_token_price_microcredits"}},
		{&model.BillingOrder{}, []string{"cache_write_token_price_microcredits", "cost_cache_write_token_price_microcredits", "cache_write_tokens"}},
		{&model.LogicalModel{}, []string{"cache_write_price_microcredits"}},
		{&model.ApiCallLog{}, []string{"cache_write_tokens"}},
	} {
		// 精简部署或旧测试库可能没有某些表；表缺失时由基线 AutoMigrate 按当前结构建表。
		if !tx.Migrator().HasTable(target.entity) {
			continue
		}
		for _, column := range target.columns {
			if tx.Migrator().HasColumn(target.entity, column) {
				continue
			}
			if err := tx.Migrator().AddColumn(target.entity, column); err != nil {
				return err
			}
		}
	}
	return nil
}

// migratePaymentOrderChargeMethod 为支付订单增加实收金额、实收货币、汇率快照和支付方式。
// 旧订单都是按商品货币原价下单，实收金额回填为商品金额，回调与对账校验结果不变。
func migratePaymentOrderChargeMethod(tx *gorm.DB) error {
	for _, field := range []string{"ChargeAmountFen", "ChargeCurrency", "ExchangeRateMicros", "PaymentMethod"} {
		if tx.Migrator().HasColumn(&model.PaymentOrder{}, field) {
			continue
		}
		if err := tx.Migrator().AddColumn(&model.PaymentOrder{}, field); err != nil {
			return err
		}
	}
	return tx.Model(&model.PaymentOrder{}).
		Where("charge_currency = '' OR charge_currency IS NULL").
		Updates(map[string]any{"charge_amount_fen": gorm.Expr("amount_fen"), "charge_currency": gorm.Expr("currency")}).Error
}

// migrateDramaCastVoiceLooks 为短剧设定补角色声音（声音库条目与样本音频）和造型（所属角色、造型名、默认适用章节）。
// 旧设定都是没有指定声音的角色本体，新增列写成零值。
func migrateDramaCastVoiceLooks(tx *gorm.DB) error {
	for _, field := range []string{"VoiceProfileID", "VoiceSampleResourceID", "ParentID", "LookName", "LookFromChapter", "LookToChapter"} {
		if tx.Migrator().HasColumn(&model.DramaCast{}, field) {
			continue
		}
		if err := tx.Migrator().AddColumn(&model.DramaCast{}, field); err != nil {
			return err
		}
	}
	for column, value := range map[string]any{"voice_profile_id": "", "voice_sample_resource_id": "", "parent_id": "", "look_name": "", "look_from_chapter": 0, "look_to_chapter": 0} {
		if err := tx.Model(&model.DramaCast{}).Where(column+" IS NULL").Update(column, value).Error; err != nil {
			return err
		}
	}
	if !tx.Migrator().HasColumn(&model.DramaShot{}, "LookCastIDsJSON") {
		if err := tx.Migrator().AddColumn(&model.DramaShot{}, "LookCastIDsJSON"); err != nil {
			return err
		}
	}
	if err := tx.Model(&model.DramaShot{}).Where("look_cast_ids_json IS NULL").Update("look_cast_ids_json", "").Error; err != nil {
		return err
	}
	if tx.Migrator().HasIndex(&model.DramaCast{}, "ParentID") {
		return nil
	}
	return tx.Migrator().CreateIndex(&model.DramaCast{}, "ParentID")
}
