package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
)

type EcommerceAgentTaskView struct {
	Run          *model.EcommerceAgentRun     `json:"task"`
	Pages        []model.EcommerceAgentPage   `json:"pages"`
	PosterCanvas *ecommerceagent.PosterCanvas `json:"posterCanvas,omitempty"`
}
type EcommerceAgentCanvasView struct {
	PosterCanvas *ecommerceagent.PosterCanvas        `json:"posterCanvas"`
	Versions     []model.EcommerceAgentCanvasVersion `json:"versions"`
}
type EcommerceAgentError struct{ Code, Message string }

func (e *EcommerceAgentError) Error() string { return e.Message }
func ecommerceErr(code, message string) error {
	return &EcommerceAgentError{Code: code, Message: message}
}

func validateEcommerceAgentRequest(req ecommerceagent.Request, generate bool) (ecommerceagent.Platform, error) {
	p, ok := ecommerceagent.PlatformFor(req.Platform)
	if !ok {
		return p, ecommerceErr("INVALID_INPUT", "不支持的电商平台")
	}
	if len(req.FileInfoList) < 1 || len(req.FileInfoList) > 5 {
		return p, ecommerceErr("INVALID_INPUT", "请提供 1 至 5 张商品图")
	}
	for _, f := range req.FileInfoList {
		if len(f.URL) > 2048 || strings.TrimSpace(f.URL) == "" {
			return p, ecommerceErr("INVALID_INPUT", "商品图地址无效")
		}
	}
	if len(req.Query) > 4000 || len(req.UserSellingPoints) > 2000 {
		return p, ecommerceErr("INVALID_INPUT", "描述过长")
	}
	if !generate {
		return p, nil
	}
	if !slices.Contains([]int{100, 101, 102, 103, 200, 400}, req.GenerateType) {
		return p, ecommerceErr("INVALID_INPUT", "生成类型无效")
	}
	if req.ImageCount < 1 || req.ImageCount > 15 || (req.GenerateType == 200 && (req.ImageCount < 5 || req.ImageCount > 15)) {
		return p, ecommerceErr("INVALID_INPUT", "页数不符合生成类型要求")
	}
	if len(req.IdempotencyKey) > 150 {
		return p, ecommerceErr("INVALID_INPUT", "幂等键过长")
	}
	if req.Channel != "" && req.Channel != "promotion" && req.Channel != "premium" {
		return p, ecommerceErr("INVALID_INPUT", "出图通道无效")
	}
	return p, nil
}

func (s *Service) ecommerceReference(userID, rawURL string) (string, []byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, "", ecommerceErr("INVALID_INPUT", "商品图地址无效")
	}
	var id string
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "resources" && parts[3] == "file" {
		id = parts[2]
	}
	if id == "" {
		if u.Scheme != "https" || u.Host == "" {
			return "", nil, "", ecommerceErr("INVALID_INPUT", "仅支持已上传素材或 HTTPS 商品图")
		}
		resource, importErr := s.ImportResourceURL(userID, rawURL, "image", 0, 0, 0)
		if importErr != nil {
			return "", nil, "", importErr
		}
		id = resource.ID
	}
	resource, stream, err := s.OpenResource(userID, id)
	if err != nil {
		return "", nil, "", err
	}
	defer stream.Close()
	if resource.Kind != "image" || resource.Size > 10<<20 {
		return "", nil, "", ecommerceErr("INVALID_INPUT", "商品图须为不超过 10 MB 的图片")
	}
	mime := resource.MimeType
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		return "", nil, "", ecommerceErr("INVALID_INPUT", "商品图格式须为 PNG、JPEG 或 WebP")
	}
	raw, err := io.ReadAll(io.LimitReader(stream, 10<<20+1))
	if err != nil {
		return "", nil, "", err
	}
	if len(raw) > 10<<20 {
		return "", nil, "", ecommerceErr("INVALID_INPUT", "商品图超过 10 MB")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), raw, mime, nil
}

func (s *Service) AnalyzeEcommerceAgent(ctx context.Context, userID string, req ecommerceagent.Request) (*ecommerceagent.Analysis, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	p, err := validateEcommerceAgentRequest(req, false)
	if err != nil {
		return nil, err
	}
	cfg := ecommerceConfig()
	// Recognition and style recommendations use the administrator's
	// recognition model, the same one the /ecommerce directions stage uses.
	cfg, err = s.ecommerceVisionText(userID, cfg)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	signatureInput, _ := json.Marshal([]any{req.FileInfoList, req.Query, req.Platform, req.Language, req.Brand, cfg.Mode, cfg.TextModel})
	signature := fmt.Sprintf("%x", sha256.Sum256(signatureInput))
	if cfg.Mode == "live" {
		if cached, err := s.cachedEcommerceAnalysis(userID, signature); cached != nil || err != nil {
			return cached, err
		}
	}
	brief, visionVerified, err := s.analyzeEcommerceProduct(ctx, userID, cfg, req)
	if err != nil {
		return nil, err
	}
	styles := []ecommerceagent.Style{{Name: "极简白底", Desc: "纯白背景、完整商品与均匀影棚光"}, {Name: "场景叙事", Desc: "真实使用环境与自然光"}, {Name: "工艺特写", Desc: "只呈现照片可见的材质和结构"}}
	recs := ecommerceagent.Recommend(brief, p, req.ImageCount)
	if cfg.Mode == "live" {
		var proposed []ecommerceagent.Recommendation
		prompt := ecommerceagent.BuildRecommendPrompt(brief, p, req.Language, styles)
		if err := s.ecommerceChatJSON(ctx, cfg, "recommend", ecommerceagent.SystemRecommend, prompt, nil, &proposed); err == nil && normalizeEcommerceRecommendations(proposed) {
			recs = proposed
		}
	}
	result := &ecommerceagent.Analysis{Brief: brief, Recommendations: recs, StyleLibrary: styles}
	if cfg.Mode == "live" && visionVerified {
		encoded, _ := json.Marshal(result)
		if err := s.repo.SaveEcommerceAgentAnalysis(&model.EcommerceAgentAnalysis{ID: newID(), UserID: userID, Signature: signature, PayloadJSON: string(encoded)}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func safeRecommendationPrompt(prompt string) bool {
	if !strings.Contains(prompt, "same product as reference image, identical shape/color/logo placement") {
		return false
	}
	lower := strings.ToLower(prompt)
	for _, bad := range []string{"best seller", "no.1", "fda approved", "price", "discount", "certified"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	return true
}

func (s *Service) HelpWriteEcommerceAgent(ctx context.Context, userID, text, platform, language string) (map[string]any, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	if len(text) > 4000 || strings.TrimSpace(text) == "" {
		return nil, ecommerceErr("INVALID_INPUT", "请输入商品和出图需求")
	}
	if _, ok := ecommerceagent.PlatformFor(platform); !ok {
		return nil, ecommerceErr("INVALID_INPUT", "不支持的电商平台")
	}
	if strings.ContainsAny(text, "\x00\r") {
		return nil, ecommerceErr("INVALID_INPUT", "描述含无效字符")
	}
	if strings.Contains(text, "枪支") || strings.Contains(text, "色情") || strings.Contains(text, "治愈疾病") {
		return map[string]any{"status": "violation", "optimized": "", "reason": "需求包含不适合生成的内容", "alternatives": []any{}}, nil
	}
	if len([]rune(strings.TrimSpace(text))) <= 4 {
		return map[string]any{"status": "vague", "optimized": "", "reason": "请补充商品外观、使用场景和目标平台", "alternatives": []any{}}, nil
	}
	cfg := ecommerceConfig()
	cfg, err := s.ecommerceConfiguredText(userID, cfg)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Mode == "mock" {
		return map[string]any{"status": "normal", "optimized": text + "；请以真实商品图为准，指定场景、光线和构图，不添加未经确认的参数。", "reason": "补充画面与真实性约束", "alternatives": []map[string]string{{"label": "白底商品", "text": text + "，纯白背景，完整商品"}, {"label": "真实场景", "text": text + "，自然光下的真实使用环境"}}}, nil
	}
	var result map[string]any
	err = s.ecommerceChatJSON(ctx, cfg, "help-write", ecommerceagent.SystemHelpWrite, fmt.Sprintf("【卖家原话】%s\n【平台】%s\n【语言】%s", text, platform, language), nil, &result)
	if err != nil {
		return nil, ecommerceErr("UPSTREAM_ERROR", "提示词优化失败")
	}
	return result, nil
}

func (s *Service) ecommerceAgentOrder(userID, runID, key string, req ecommerceagent.Request, cfg ecommerceProviderConfig, count int) (*model.BillingOrder, int64, error) {
	if cfg.Mode == "mock" {
		unit := int64(32000)
		if req.ModelKey == "gpt-image-2-2K" {
			unit = 61000
		}
		if req.Channel == "premium" {
			unit = unit * 16 / 10
		}
		amount, _, _, chargeErr := ecommerceagent.Charge(unit, count, count)
		if chargeErr != nil {
			return nil, 0, chargeErr
		}
		modelKey := req.ModelKey
		if modelKey == "" {
			modelKey = cfg.ImageModel
		}
		order := &model.BillingOrder{ID: newID(), UserID: userID, TaskID: runID, IdempotencyKey: "ecommerce:" + key, Model: modelKey, Capability: "image", Scene: "ecommerce_agent", BillingMode: "fixed_request", UnitPriceMicrocredits: unit, MultiplierBasisPoints: 10000, Quantity: int64(count), AmountMicrocredits: amount, ReservedAmountMicrocredits: amount, Status: model.BillingStatusReserved}
		return order, unit, nil
	}
	channel := req.Channel
	if channel == "" {
		channel = "promotion"
	}
	channelID := strings.TrimSpace(os.Getenv("ECOMMERCE_" + strings.ToUpper(channel) + "_CHANNEL_ID"))
	if channelID == "" {
		return nil, 0, ecommerceErr("INVALID_INPUT", "尚未配置电商出图通道")
	}
	modelKey := strings.TrimSpace(req.ModelKey)
	if modelKey == "" {
		modelKey = cfg.ImageModel
	}
	intent := ModelRequestIntent{Capability: "image", Operation: "image_to_image", Inputs: map[string]int{"image": 1}, Options: map[string]any{}}
	order, err := s.newBillingOrderWithPriceTier(userID, runID, "ecommerce:"+key, channelID, modelKey, "image", "ecommerce_agent", 1, tokenBillingEstimate{}, "", intent)
	if err != nil {
		return nil, 0, err
	}
	if order.BillingMode != "fixed_request" || order.AmountMicrocredits <= 0 {
		return nil, 0, ecommerceErr("INVALID_INPUT", "电商图像模型须配置单次固定积分价格")
	}
	unit := order.AmountMicrocredits
	amount, _, _, chargeErr := ecommerceagent.Charge(unit, count, count)
	if chargeErr != nil {
		return nil, 0, chargeErr
	}
	order.Quantity = int64(count)
	order.CostQuantity = int64(count)
	order.AmountMicrocredits = amount
	order.ReservedAmountMicrocredits = order.AmountMicrocredits
	order.ChargeLimitSet = true
	order.ChargeLimitMicrocredits = order.AmountMicrocredits
	return order, unit, nil
}

func (s *Service) QuoteEcommerceAgent(userID string, req ecommerceagent.Request) (map[string]any, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	if err := s.RequireFeature(FeatureImageWorkbench); err != nil {
		return nil, err
	}
	platform, err := validateEcommerceAgentRequest(req, true)
	if err != nil {
		return nil, err
	}
	cfg := ecommerceConfig()
	if cfg.Mode == "live" {
		cfg, err = s.ecommerceLiveImageConfig(req, cfg)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Mode != "live" && cfg.Mode != "mock" {
		return nil, ecommerceErr("UNAVAILABLE", "电商 Agent 出图尚未配置")
	}
	count := len(ecommerceagent.Plan(req.ImageCount, req.GenerateType, platform, nil, ecommerceagent.Brief{}))
	order, unit, err := s.ecommerceAgentOrder(userID, newID(), newID(), req, cfg, count)
	if err != nil {
		return nil, err
	}
	return map[string]any{"pageCount": count, "unitMicrocredits": unit, "totalMicrocredits": order.AmountMicrocredits, "modelKey": order.Model, "channel": req.Channel}, nil
}

func (s *Service) ecommerceLiveImageConfig(req ecommerceagent.Request, cfg ecommerceProviderConfig) (ecommerceProviderConfig, error) {
	channel := req.Channel
	if channel == "" {
		channel = "promotion"
	}
	channelID := strings.TrimSpace(os.Getenv("ECOMMERCE_" + strings.ToUpper(channel) + "_CHANNEL_ID"))
	if channelID == "" {
		return cfg, ecommerceErr("INVALID_INPUT", "尚未配置电商出图通道")
	}
	modelKey := strings.TrimSpace(req.ModelKey)
	if modelKey == "" {
		modelKey = cfg.ImageModel
	}
	configured, err := s.SystemChannel(channelID)
	if err != nil {
		return cfg, ecommerceErr("INVALID_INPUT", "出图通道不可用")
	}
	if strings.TrimSpace(configured.APIKey) == "" {
		return cfg, ecommerceErr("INVALID_INPUT", "出图通道缺少服务端密钥")
	}
	u, err := url.Parse(strings.TrimSpace(configured.BaseURL))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return cfg, ecommerceErr("INVALID_INPUT", "出图通道地址无效")
	}
	configuredModel, err := s.repo.ChannelModelByKey(channelID, modelKey)
	if err != nil || !configuredModel.Enabled || configuredModel.Capability != "image" {
		return cfg, ecommerceErr("INVALID_INPUT", "图像模型未启用")
	}
	if configuredModel.Protocol != model.ChannelInterfaceOpenAIImage {
		return cfg, ecommerceErr("INVALID_INPUT", "电商 Agent 当前只支持 OpenAI Images 编辑协议的图像渠道")
	}
	profile, err := normalizedChannelModelCapability(configuredModel, s.protocolRegistry())
	if err != nil || profile == nil || profile.Image == nil || profile.Image.References.MaxImages < 1 {
		return cfg, ecommerceErr("INVALID_INPUT", "图像模型未配置商品图编辑能力")
	}
	provider, err := s.resolveProviderConfig(providerConfig{ChannelID: channelID, ChannelModelKey: modelKey, Model: modelKey})
	if err != nil {
		return cfg, ecommerceErr("INVALID_INPUT", "出图渠道配置不可用")
	}
	provider.CapabilityConfig = profile
	cfg.ImageBaseURL = strings.TrimRight(configured.BaseURL, "/")
	cfg.ImageKey = configured.APIKey
	cfg.ImageModel = modelKey
	cfg.ImageProvider = &provider
	if configuredModel.ProviderModelKey != "" {
		cfg.ImageModel = configuredModel.ProviderModelKey
	}
	return cfg, nil
}

func (s *Service) ecommerceAgentGeneratedURL(userID string, data []byte, rawURL, ratio string) (string, error) {
	var err error
	if len(data) == 0 && rawURL != "" {
		_, data, _, err = s.ecommerceReference(userID, rawURL)
		if err != nil {
			return "", err
		}
	}
	if len(data) == 0 {
		return "", errors.New("图像模型没有返回图片")
	}
	data, err = ecommerceagent.PadImageToRatio(data, ratio)
	if err != nil {
		return "", err
	}
	resource, err := s.UploadResourceFile(userID, "ecommerce-agent.png", int64(len(data)), "image", 0, 0, 0, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return "/api/resources/" + resource.ID + "/file", nil
}

func (s *Service) EcommerceAgentTask(userID, runID string) (*EcommerceAgentTaskView, error) {
	run, err := s.repo.EcommerceAgentRun(userID, runID)
	if err != nil {
		return nil, err
	}
	pages, err := s.repo.EcommerceAgentPages(userID, runID)
	if err != nil {
		return nil, err
	}
	view := &EcommerceAgentTaskView{Run: run, Pages: pages}
	if run.CanvasJSON != "" {
		var canvas ecommerceagent.PosterCanvas
		if json.Unmarshal([]byte(run.CanvasJSON), &canvas) == nil {
			view.PosterCanvas = &canvas
		}
	}
	return view, nil
}

func (s *Service) LatestEcommerceAgentProjectTask(userID, projectID string) (*EcommerceAgentTaskView, error) {
	run, err := s.repo.LatestEcommerceAgentRunByProject(userID, projectID)
	if err != nil {
		return nil, err
	}
	return s.EcommerceAgentTask(userID, run.ID)
}
func (s *Service) CancelEcommerceAgent(userID, runID string) (*EcommerceAgentTaskView, error) {
	run, err := s.repo.EcommerceAgentRun(userID, runID)
	if err != nil {
		return nil, err
	}
	if run.Status == "running" {
		if err := s.repo.RequestEcommerceAgentCancel(userID, runID); err != nil {
			return nil, err
		}
		s.cancelActiveTask(runID)
	}
	return s.EcommerceAgentTask(userID, runID)
}
func (s *Service) SaveEcommerceAgentCanvas(userID, projectID string, canvas ecommerceagent.PosterCanvas) (*model.EcommerceAgentCanvasVersion, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	if canvas.CanvasProtocol != "SNAPSHOT" || len(canvas.Objects) > 500 || canvas.Width < 1 || canvas.Height < 1 {
		return nil, ecommerceErr("INVALID_INPUT", "画布格式无效")
	}
	encoded, err := json.Marshal(canvas)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 2<<20 {
		return nil, ecommerceErr("INVALID_INPUT", "画布超过 2 MB")
	}
	return s.repo.SaveEcommerceAgentCanvas(userID, projectID, string(encoded))
}
func (s *Service) EcommerceAgentCanvas(userID, projectID string) (*EcommerceAgentCanvasView, error) {
	versions, err := s.repo.EcommerceAgentCanvas(userID, projectID)
	if err != nil {
		return nil, err
	}
	view := &EcommerceAgentCanvasView{Versions: versions}
	if len(versions) > 0 {
		var canvas ecommerceagent.PosterCanvas
		if err = json.Unmarshal([]byte(versions[0].CanvasJSON), &canvas); err != nil {
			return nil, err
		}
		view.PosterCanvas = &canvas
	}
	return view, nil
}
