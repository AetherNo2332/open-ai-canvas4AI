package ecommerceagent

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// DesignPlaybookVersion identifies the design knowledge base that drew a run's
// layouts, UI kit, typography, persona and scenes.
const DesignPlaybookVersion = "meiye-design-playbook-20261031-v1"

// PlatformGuide is the art-direction brief of one marketplace family: how its
// buyers read images, which layouts and UI suit it, what to avoid, and which
// style-library routes fit it.
type PlatformGuide struct {
	Key, Label, Tone, Layout, UIKit, Avoid string
	Routes                                 []string
}

// platformGuides is keyed by family. Labels name the family; PlatformGuideFor
// prefixes the concrete platform name.
var platformGuides = map[string]PlatformGuide{
	"xiaohongshu": {
		Key: "xiaohongshu", Label: "种草笔记",
		Tone:   "像真实用户的生活分享：先给情绪和场景，再给理由；标题口语、有观点，可用清单或对比句式，不喊广告口号。",
		Layout: "3:4 竖版笔记封面感：上部约三分之一放两行大字标题，下部是手机实拍感的生活场景（自然窗光、浅景深）；内页按开箱、前后对比、教程步骤、好物清单组织，一张只讲一件事。",
		UIKit:  "手写批注与箭头、便签纸、圆角贴纸、荧光笔划线、相纸白边与胶带；低饱和胶片色或奶油色调，主色不超过三种。",
		Avoid:  "硬广促销角标、价格爆炸贴、过度精修的棚拍感与密集参数表；不伪造评价、点赞与“亲测有效”。",
		Routes: []string{"安静治愈", "复古胶片", "杂志编辑", "自然有机", "多巴胺撞色", "手绘童趣"},
	},
	"short-video": {
		Key: "short-video", Label: "内容电商",
		Tone:   "停留逻辑优先：第一眼就要有钩子，大字直接说出场景痛点或惊喜点，人物表情与动作要强，节奏像短视频封面。",
		Layout: "粗描边大字钩子横跨画面上部、宽度约占 70% 以上；人物手持或正在使用商品，动作幅度大但自然；用动感斜切、前后对比与局部放大制造爆点，配色高对比高饱和。",
		UIKit:  "爆炸星形贴、粗描边大字、箭头与圈注、速度线与闪光；福利点只写卖家提供的真实赠品或规格。",
		Avoid:  "编造价格、限时、销量与“全网最低”；低清抠图与廉价渐变；大字遮挡商品主体或人物面部。",
		Routes: []string{"痛点对比", "电音赛博", "运动机能", "多巴胺撞色", "潮流街头", "节日礼赠"},
	},
	"shelf": {
		Key: "shelf", Label: "货架电商",
		Tone:   "买家带着需求来比较：首屏一眼看懂“是什么、好在哪、适合谁”，品牌旗舰店质感，信息层级清楚可信。",
		Layout: "首屏卖点总览：品牌主标题 + 商品大图 + 3–4 个图标卖点气泡与引线；后续轮换场景大图、细节放大窗、规格卡与对比分栏；主图商品约占画面 60–70%，详情竖版连续阅读。",
		UIKit:  "统一线宽的图标、卖点气泡、标签条、编号与规格卡；天猫偏品牌留白，京东偏理性参数与对比，淘宝可更生活化。",
		Avoid:  "极限词（最、第一、国家级）；虚构原价、销量与好评；促销角标遮挡商品；深色压抑背景让移动端小图看不清。",
		Routes: []string{"极简留白", "轻奢质感", "硬核实证", "温情陪伴", "国潮新中式", "都市摩登"},
	},
	"pdd": {
		Key: "pdd", Label: "直给性价比",
		Tone:   "买家快速滑动比价：必须一眼看清商品本身、件数与组合，利益点直接上图，不绕弯。",
		Layout: "商品完整放大、背景明亮干净；多件装、多色、多规格用整齐的组合陈列或宫格“一图看全”；件数与规格用大号数字标签；主图少用氛围大片，场景留给副图。",
		UIKit:  "暖红橙利益点徽章、圆角大号数字标签、勾选图标与简洁分栏；粗黑体、强对比。",
		Avoid:  "编造价格、补贴、销量与包邮承诺；暗调与过度留白的奢侈品式画面；借用其他品牌元素。",
		Routes: []string{"痛点对比", "多巴胺撞色", "活力萌趣", "温情陪伴", "节日礼赠"},
	},
	"wholesale": {
		Key: "wholesale", Label: "批发采购",
		Tone:   "买家是店主与采购：看工厂实力、规格齐全、供货稳定与拿样效率，信息要专业可信。",
		Layout: "确有多款时用多 SKU 整齐陈列墙与颜色/规格矩阵，单款用多角度与细节；批量堆叠与装箱实拍、产线或仓储场景；参数表清楚，起订量与阶梯价只写卖家数据。",
		UIKit:  "白底信息卡、表格线框、编号、色卡与简洁线性图标；工业蓝灰或品牌色，装饰极少。",
		Avoid:  "虚构产能、厂房面积、认证与合作品牌；用零售情绪大片替代规格信息。",
		Routes: []string{"硬核实证", "极简留白", "都市摩登", "痛点对比"},
	},
	"vipshop": {
		Key: "vipshop", Label: "品牌特卖",
		Tone:   "正品感与品牌调性优先：画面像品牌官方画册，穿搭与生活方式感强。",
		Layout: "模特穿搭或生活方式大图 + 品牌与系列名 + 精简卖点；确有多色多款时整齐陈列；留白克制，标题字体讲究。",
		UIKit:  "细线框标签、极简图标、编号与品牌色块，不用爆炸贴。",
		Avoid:  "虚构折扣力度、原价与限时；低质感拼贴与杂乱背景。",
		Routes: []string{"轻奢质感", "杂志编辑", "都市摩登", "极简留白", "节日礼赠"},
	},
	"dewu": {
		Key: "dewu", Label: "潮流品质",
		Tone:   "年轻潮流买家看真实细节、做工和上脚上身效果，审美偏街头、球鞋与潮玩文化。",
		Layout: "暗场或街头场景的英雄大图、多角度细节特写（鞋底、缝线、标签、材质纹理）、上脚或上身效果、侧面平视陈列；画面干净、质感硬朗。",
		UIKit:  "鞋盒标签、尺码条、编号、细线引线与方形取景框放大窗，条形码元素点缀。",
		Avoid:  "卖家未提供时出现“鉴定”“正品保证”等平台背书字样；虚构联名与限量。",
		Routes: []string{"潮流街头", "电音赛博", "Y2K 千禧", "硬核实证", "杂志编辑"},
	},
	"aikucun": {
		Key: "aikucun", Label: "社交分销",
		Tone:   "素材要方便店主转发：一图看清品牌、商品与卖点，真实可信、亲和好分享。",
		Layout: "商品清楚居中 + 品牌名 + 3 个短卖点；确有多色多款时做合集图；生活化场景配一句分享口吻的文案。",
		UIKit:  "圆角卡片、简洁图标、品牌色标签与轻量贴纸。",
		Avoid:  "虚构价格、佣金与销量；信息过密难以转发阅读。",
		Routes: []string{"温情陪伴", "痛点对比", "多巴胺撞色", "极简留白", "节日礼赠"},
	},
	"railway": {
		Key: "railway", Label: "旅途礼赠",
		Tone:   "面向旅客与出行人群：地方特产、文创礼品与旅途好物，调性稳重可信，有地域文化与礼赠感。",
		Layout: "旅途场景（车厢小桌板、站台、行李架）与地域风物结合；礼盒陈列，产地风光虚化作背景；标题端正、信息清楚。",
		UIKit:  "印章与票根式标签、线描风物图标、铁路线路式引线与简洁信息卡。",
		Avoid:  "虚构地理标志、获奖与官方合作；过度潮流或夸张的视觉。",
		Routes: []string{"国潮新中式", "节日礼赠", "自然有机", "都市摩登", "轻奢质感"},
	},
	"wechat": {
		Key: "wechat", Label: "熟人私域",
		Tone:   "像朋友真实分享的好物：亲切可信，突出真实使用场景和人。",
		Layout: "真实生活场景与人物使用，温暖自然光；首图商品与人同框，标题像一句推荐语；单张信息量适合朋友圈与群聊转发。",
		UIKit:  "手写感短句、圆角对话气泡、简洁图标、相纸白边。",
		Avoid:  "伪造聊天截图、好评与成交记录；夸张促销与刷屏式大字。",
		Routes: []string{"温情陪伴", "安静治愈", "自然有机", "复古胶片", "节日礼赠"},
	},
	"listing": {
		Key: "listing", Label: "跨境 Listing",
		Tone:   "买家只能靠图片判断商品：主图合规优先，副图逐张回答一个购买问题；目标语言短而具体。",
		Layout: "主图纯白 RGB 255、商品完整约占画面 85%、无文字水印与道具；副图依次用信息图（3–4 个图标卖点）、生活方式场景、尺寸对比（数值只用卖家资料）、细节特写与包装内容；A+ 用横幅模块分段讲品牌故事。",
		UIKit:  "白底或浅灰信息图、统一线性图标、细引线与圆形放大窗、目标语言短标题。",
		Avoid:  "主图加文字、Logo 水印、促销标与边框；副图写 Best Seller、No.1、认证等未授权宣称；机械直译中文口号。",
		Routes: []string{"极简留白", "硬核实证", "都市摩登", "自然有机", "温情陪伴"},
	},
	"indie": {
		Key: "indie", Label: "品牌独立站",
		Tone:   "讲品牌故事与生活方式：像杂志编辑摄影，克制、有观点、有审美门槛。",
		Layout: "全幅编辑摄影 + 大留白 + 小号衬线标题；商品与环境、人物的关系像杂志内页；横版 Banner 与竖版内容图组合，系列色调统一。",
		UIKit:  "极简文字排版、细分隔线、编号与页码式标注，几乎不用图标与贴纸。",
		Avoid:  "平台式促销角标、爆炸贴与密集卖点气泡；廉价素材感背景。",
		Routes: []string{"杂志编辑", "自然有机", "复古胶片", "轻奢质感", "安静治愈", "极简留白"},
	},
	"mobile-cross": {
		Key: "mobile-cross", Label: "跨境移动电商",
		Tone:   "小屏快速滑动：商品要大、对比要强，目标语言短标题一眼读懂。",
		Layout: "商品约占画面 60–80%，背景纯色或简洁场景、高对比；目标语言 2–5 个词的大标题；使用场景与人物动作直观；俄语、德语等长单词预留字号余量，阿拉伯语从右到左排版。",
		UIKit:  "粗描边标签、圆形利益点徽章、大号数字、箭头与对比分栏；TikTok Shop 像内容封面，Shopee、Lazada 明快高饱和。",
		Avoid:  "虚构折扣、包邮与销量；主图白底或无字要求按平台官方执行；画面里残留中文。",
		Routes: []string{"多巴胺撞色", "痛点对比", "运动机能", "活力萌趣", "潮流街头", "科技未来"},
	},
	"b2b-global": {
		Key: "b2b-global", Label: "跨境 B2B",
		Tone:   "海外采购商看供货能力、定制能力与规格完整度，画面专业可信。",
		Layout: "白底或浅灰棚拍的多 SKU 阵列（确有多款时）、规格表、材质与工艺细节、包装与装箱方式、定制选项（仅卖家提供时）；英文标题简洁。",
		UIKit:  "表格线框、编号、色卡、简洁线性图标与品牌色条。",
		Avoid:  "虚构工厂实力、认证与客户案例；零售促销语言。",
		Routes: []string{"硬核实证", "极简留白", "都市摩登", "科技未来"},
	},
	"default": {
		Key: "default", Label: "通用电商",
		Tone:   "忠于商品资料与目标语言：画面清楚、可信，同时要有品牌设计感。",
		Layout: "主图商品完整清楚；副图与详情轮换场景大图、细节放大窗、信息卡与对比分栏；移动端优先。",
		UIKit:  "按已选方向统一标签、图标、引线与装饰，整套一致。",
		Avoid:  "推测未核实的平台强制规则；编造价格、认证、参数与功效。",
		Routes: []string{"极简留白", "温情陪伴", "杂志编辑", "痛点对比", "科技未来"},
	},
}

// platformFamilies maps workbench platform ids (and common Chinese names) to a
// guide family.
var platformFamilies = map[string]string{
	"shein": "indie", "希音": "indie",
	"xiaohongshu": "xiaohongshu", "小红书": "xiaohongshu", "小红书电商": "xiaohongshu",
	"douyin": "short-video", "kuaishou": "short-video", "抖音": "short-video", "抖音电商": "short-video", "快手": "short-video", "快手电商": "short-video",
	"taobao": "shelf", "tmall": "shelf", "jd": "shelf", "淘宝": "shelf", "天猫": "shelf", "京东": "shelf",
	"pdd": "pdd", "pinduoduo": "pdd", "拼多多": "pdd",
	"1688":    "wholesale",
	"vipshop": "vipshop", "唯品会": "vipshop",
	"dewu": "dewu", "得物": "dewu",
	"aikucun": "aikucun", "爱库存": "aikucun",
	"railway": "railway", "国铁商城": "railway",
	"wechat-shop": "wechat", "video-shop": "wechat", "微信小店": "wechat", "视频号小店": "wechat",
	// The listing family is the same set as the web's platform-style.ts
	// familyOf "listing": its main images are pure white without text.
	"amazon": "listing", "亚马逊": "listing", "ebay": "listing", "walmart": "listing", "aliexpress": "listing", "速卖通": "listing",
	"coupang": "listing", "mercado-libre": "listing", "emag": "listing", "wayfair": "listing", "noon": "listing",
	"shopify": "indie", "etsy": "indie", "独立站": "indie",
	"tiktok": "mobile-cross", "tiktok-shop": "mobile-cross", "temu": "mobile-cross", "shopee": "mobile-cross", "lazada": "mobile-cross",
	"ozon": "mobile-cross", "wildberries": "mobile-cross", "rakuten": "mobile-cross", "11street": "mobile-cross",
	"alibaba": "b2b-global", "阿里巴巴国际站": "b2b-global", "dhgate": "b2b-global",
}

var platformDisplayNames = map[string]string{
	"shein":  "SHEIN（希音）",
	"taobao": "淘宝", "tmall": "天猫", "jd": "京东", "pdd": "拼多多", "pinduoduo": "拼多多", "douyin": "抖音电商", "kuaishou": "快手电商", "1688": "1688", "xiaohongshu": "小红书电商",
	"amazon": "Amazon", "temu": "Temu", "shopee": "Shopee", "lazada": "Lazada", "tiktok": "TikTok Shop", "tiktok-shop": "TikTok Shop", "shopify": "Shopify", "ebay": "eBay", "aliexpress": "AliExpress",
	"wechat-shop": "微信小店", "video-shop": "视频号小店", "etsy": "Etsy", "walmart": "Walmart", "mercado-libre": "Mercado Libre", "coupang": "Coupang", "rakuten": "Rakuten", "ozon": "Ozon",
	"alibaba": "阿里巴巴国际站", "dhgate": "DHgate", "11street": "11Street", "wayfair": "Wayfair", "noon": "Noon", "wildberries": "WBPartners", "emag": "eMAG",
	"vipshop": "唯品会", "dewu": "得物", "aikucun": "爱库存", "railway": "国铁商城",
}

// crossBorderFamilies cast models for the target market instead of a domestic
// default.
var crossBorderFamilies = map[string]bool{"listing": true, "indie": true, "mobile-cross": true, "b2b-global": true}

func platformKey(platformID string) string { return strings.ToLower(strings.TrimSpace(platformID)) }

func platformFamily(platformID string) string {
	if family, ok := platformFamilies[platformKey(platformID)]; ok {
		return family
	}
	return "default"
}

// PlatformGuideFor returns the guide of the platform's family, labelled with
// the platform's own name. Unknown platforms get the neutral default guide.
func PlatformGuideFor(platformID string) PlatformGuide {
	id := platformKey(platformID)
	guide := platformGuides[platformFamily(id)]
	guide.Routes = append([]string(nil), guide.Routes...)
	if p, ok := PlatformKnowledgeFor(platformID); ok {
		guide.Tone = p.Style
		guide.Avoid += "；" + p.Risk
		if !p.WhiteMain && guide.Key == "listing" {
			guide.Layout = "真实商品多角度摄影，背景按具体类目核实，不套用Amazon的纯白占比；不掩盖缺陷。"
		}
		if p.AllListingNoText {
			guide.Layout = "商品刊登图采用客观真实摄影，不添加标题、营销图形或信息图；独立社媒封面另按用途处理。"
			guide.UIKit = "商品刊登图不叠加UI、水印或边框。"
		}
		if p.ID == "shein" {
			guide.Key, guide.Label = "indie", "时尚目录"
			guide.Layout = "服饰用正背面版型、自然上身、面料细节；非服饰用清楚的用途与细节摄影。主图不叠加文案，副图按站点类目核实。"
			guide.UIKit = "克制留白与统一色彩，不用爆炸贴、平台徽章或价格贴。"
			guide.Routes = []string{"杂志编辑", "潮流街头", "极简留白", "自然有机"}
		}
	}
	name := platformDisplayNames[id]
	if name == "" {
		name = strings.TrimSpace(platformID)
	}
	if name != "" {
		guide.Label = name + "（" + guide.Label + "）"
	}
	return guide
}

// ListingMainImage reports a cross-border listing main image (Amazon, eBay,
// Walmart, AliExpress …): pure white with no text, so its copy stays empty.
// It mirrors the web's commercePlatformListingMain: a main image, or a single
// image whose purpose is a main image, on a listing-family platform.
func ListingMainImage(platformID, imageType, purpose string) bool {
	if p, ok := PlatformKnowledgeFor(platformID); ok {
		return p.WhiteMain && (imageType == "main" || (imageType == "single" && strings.Contains(purpose, "主图")))
	}
	if platformFamily(platformID) != "listing" {
		return false
	}
	kind := strings.TrimSpace(imageType)
	return kind == "main" || (kind == "single" && strings.Contains(purpose, "主图"))
}

// directionsTerm names a content type in the fast directions prompt: the
// content label, the narrative field description and an optional type rule.
type directionsTerm struct{ label, narrative, rule string }

var directionsTerms = map[string]directionsTerm{
	"detail":         {label: "整套详情页", narrative: "九屏节奏一句话"},
	"set":            {label: "产品套图", narrative: "套图张与张的分工一句话", rule: "【产品套图】每套方向都是一整组不同用途商品图（主图、卖点、细节、场景、规格、收束）共用的美术方案，不是某一张的内容。"},
	"main-secondary": {label: "主副图组合", narrative: "主图与副图的分工一句话", rule: "【主副图组合】每套方向是一组“主图 + 副图”的统一美术方案：主图服从平台主图规则（要求白底或无字时照做），副图承担场景或卖点补充；不要把主图和副图拆成两套方向。"},
	"main":           {label: "商品主图", narrative: "主图的画面焦点与候选差异一句话", rule: "【商品主图】平台要求白底或无字时，四套在合规前提下只拉开布光、角度、陈列与道具的差异。"},
	"secondary":      {label: "商品副图", narrative: "副图与主图的信息分工一句话"},
	"single":         {label: "单张成品", narrative: "单图的画面焦点一句话", rule: "【单张成品】每套方向是一张独立成品的美术方案，按用户所选用途（主图、副图、Banner、SKU 展示、场景图）设计画面焦点。"},
	"marketing":      {label: "营销物料", narrative: "物料的信息主次一句话", rule: "【营销物料】每套方向是一套可延展到用户所选物料（海报、Banner、信息流、社媒封面等）的广告美术方案；活动、价格与权益只用用户提供的真实信息。"},
	"white":          {label: "白底图", narrative: "白底陈列的光影与角度一句话", rule: "【白底图专属，优先于上面第 2–4 条】四套都必须是纯白背景（RGB 255）、无道具、无新增文字的完整商品图，只在布光（柔光箱、侧逆光、顶光）、阴影（接触阴影、柔投影、无影）、角度（正面、三分之四、俯拍）与陈列（单件、组合、悬浮）上拉开差异；palette 写“纯白 #FFFFFF”与商品本色，typography 写“不新增文字”，graphics 写“不新增装饰”，label 用光影词开头（如“柔光轻影”“侧光立体”）。"},
	"three-view":     {label: "三视图", narrative: "三视图的视角编排与光影一句话", rule: "【三视图专属，优先于上面第 2–4 条】四套都保持纯白背景、无道具、无新增文字，视角按用户设定的正面、侧面、背面等编排，只在布光、阴影与视图排列方式上区分；palette 写纯白与商品本色，typography 写“不新增文字”，graphics 写“不新增装饰”。"},
	"sku":            {label: "SKU 商品图", narrative: "SKU 之间统一与区分的方式一句话", rule: "【SKU 图】每套方向是一套全 SKU 统一的美术方案：同一背景、主体尺度与文案位置，只在整体风格上区分。"},
	"scene":          {label: "使用场景图", narrative: "场景与人物关系一句话"},
	"selling":        {label: "卖点图", narrative: "卖点逐张呈现的方式一句话"},
	"feature":        {label: "功能图", narrative: "功能演示的顺序一句话"},
	"size":           {label: "尺寸展示图", narrative: "尺寸标注的视角与层级一句话"},
	"material":       {label: "材质展示图", narrative: "材质呈现的尺度一句话"},
	"package":        {label: "包装展示图", narrative: "包装展示的状态一句话"},
	"poster":         {label: "活动海报", narrative: "海报的信息主次一句话"},
	"amazon-a-plus":  {label: "Amazon A+ 模块", narrative: "A+ 模块的叙事顺序一句话"},
	"social":         {label: "社交推广图", narrative: "社交封面的抓眼点一句话"},
}

func directionsTermFor(imageType string) directionsTerm {
	if term, ok := directionsTerms[strings.TrimSpace(imageType)]; ok {
		return term
	}
	return directionsTerm{label: "整组商品图", narrative: "整组节奏一句话"}
}

// FastDirectionsSystemFor adapts the fast directions prompt to the content
// type. Detail keeps WorkbenchFastDirectionsSystem unchanged; other types name
// their own content and narrative, and white types stay on pure white.
// "main-secondary" is the 主副图 quick type (a two-image set).
func FastDirectionsSystemFor(imageType string) string {
	kind := strings.TrimSpace(imageType)
	if kind == "" || kind == "detail" {
		return WorkbenchFastDirectionsSystem
	}
	term := directionsTermFor(kind)
	system := strings.Replace(WorkbenchFastDirectionsSystem, "整套详情页视觉方向", term.label+"视觉方向", 1)
	system = strings.Replace(system, `"narrative":"九屏节奏一句话"`, `"narrative":"`+term.narrative+`"`, 1)
	if term.rule != "" {
		system = strings.Replace(system, "只输出一个 JSON 对象", term.rule+"\n只输出一个 JSON 对象", 1)
	}
	return system
}

// platformTypeRule is the one platform rule that changes what the four
// directions may do for a content type.
func platformTypeRule(family, kind string) string {
	listingMain := kind == "main" || kind == "set" || kind == "main-secondary"
	switch {
	case kind == "white" || kind == "three-view":
		return "白底类型在任何平台都保持纯白、无道具、无新增文字，四套只在布光、阴影、角度与陈列上区分。"
	case family == "listing" && listingMain:
		return "主图须纯白 RGB 255、商品约占画面 85%、无文字水印与道具：四套的主图只在布光、角度与陈列上区分，风格反差放到副图与场景图。"
	case family == "pdd" && listingMain:
		return "首图让商品与组合一眼看清，件数与规格只写卖家提供的真实信息。"
	case family == "xiaohongshu":
		return "首图按笔记封面思路设计：留出两行大字标题区，画面像真实生活分享。"
	case family == "short-video":
		return "首图像短视频封面：第一眼就有钩子大字或强动作。"
	}
	return ""
}

// directionFamilies are the four style families of the fast directions prompt
// (静, 燃, 潮, 都市; the lists match WorkbenchFastDirectionsSystem) with the
// style-library routes each one covers.
var directionFamilies = []struct {
	name   string
	routes []string
}{
	{"静", []string{"安静治愈", "极简留白", "自然有机", "温情陪伴"}},
	{"燃", []string{"电音赛博", "科技未来", "硬核实证", "运动机能"}},
	{"潮", []string{"潮流街头", "多巴胺撞色", "活力萌趣", "国潮新中式", "Y2K 千禧", "手绘童趣", "痛点对比", "节日礼赠"}},
	{"都市", []string{"都市摩登", "轻奢质感", "复古胶片", "杂志编辑"}},
}

// familyRoutes groups a platform's suitable routes by direction family, e.g.
// "静＝安静治愈、自然有机；潮＝多巴胺撞色". Families without a route are left out.
func familyRoutes(routes []string) string {
	var parts []string
	for _, family := range directionFamilies {
		var picked []string
		for _, route := range routes {
			if slices.Contains(family.routes, route) {
				picked = append(picked, route)
			}
		}
		if len(picked) > 0 {
			parts = append(parts, family.name+"＝"+strings.Join(picked, "、"))
		}
	}
	return strings.Join(parts, "；")
}

// directionsRouteRule makes each family start from this platform's routes
// instead of the same four archetypes everywhere, and keeps routes that do
// not suit the category (neon for a baby bottle) out.
const directionsRouteRule = "选路线：四个家族各出一套，每套先从本平台该家族的路线里选适合商品的一条，label 以该路线名开头；家族没列出路线或列出的不适合商品时，再从该家族其他路线里选。按品类避让：母婴、食品、宠物不用电音赛博或赛博霓虹，燃家族改用明亮克制的硬核实证或科技未来。"

// PlatformDirectionsBrief tells the directions model how the platform and the
// content type shape the four directions, within 500 runes.
func PlatformDirectionsBrief(platformID, imageType string) string {
	guide := PlatformGuideFor(platformID)
	term := directionsTermFor(imageType)
	head := "【平台与类型适配】" + guide.Label + " × " + term.label + "\n调性：" + guide.Tone + "\n适合路线（按家族）：" + familyRoutes(guide.Routes) + "\n" + directionsRouteRule + "\n"
	tail := ""
	if rule := platformTypeRule(platformFamily(platformID), strings.TrimSpace(imageType)); rule != "" {
		tail = rule + "\n"
	}
	tail += "四套都贴合该平台的阅读习惯与" + term.label + "的用途，保持四个风格家族的强反差；平台规则以官方当期要求为准。"
	body := "版式与 UI：" + guide.Layout + guide.UIKit + "\n避免：" + guide.Avoid
	if budget := 500 - utf8.RuneCountInString(head+tail) - 1; budget > 1 {
		head += clipRunes(body, budget) + "\n"
	}
	return clipRunes(head+tail, 500)
}

// clipRunes keeps at most limit runes, ending with an ellipsis when cut.
func clipRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:max(limit, 0)])
	}
	return string(runes[:limit-1]) + "…"
}

var directionsInspirations = []string{
	"一天中的特定时刻（清晨、午后、深夜）",
	"材质与光线的关系",
	"使用者的一个情绪瞬间",
	"城市、自然与室内空间的反差",
	"色温与撞色的对比",
	"季节与节令",
	"日常生活里的仪式感",
	"商品造型的几何线条",
	"温度、声音或触感的通感联想",
	"旅行与移动中的场景",
}

// DirectionsInspirationLine is a one-line random brief appended to the fast
// directions prompt, so a repeated request never sends identical text and the
// four directions start from a new angle.
func DirectionsInspirationLine(r *rand.Rand) string {
	r = designRand(r)
	angle := directionsInspirations[r.IntN(len(directionsInspirations))]
	return fmt.Sprintf("【本轮灵感 #%06x】至少一套方向从「%s」切入；每次请求都重新构思，四套不套用常见模板。", r.Uint32()&0xffffff, angle)
}

func designRand(r *rand.Rand) *rand.Rand {
	if r != nil {
		return r
	}
	now := uint64(time.Now().UnixNano())
	return rand.New(rand.NewPCG(now, now^0x9e3779b97f4a7c15))
}

// designOption is one playbook entry. Tags steer selection:
//   - "people": shows a person or hands, only drawn when people are allowed;
//   - "text": the concept depends on on-image text, skipped in no-text runs
//     while other entries remain;
//   - "white": a pure-white, text-free product image;
//   - "multi": needs several real colours, SKUs or pieces (全家福, 多色陈列,
//     SKU 阵列, 款式目录 …); only drawn when VariationInput.MultiVariant is set,
//     so a single product never gets invented variants;
//   - "p:<family>": only for these platform families;
//   - "x:<family>": never for this platform family;
//   - "c:<category>": only for these product categories;
//   - "f:<family>": preferred for this platform family;
//   - "s:<style group>": fits these style-library families (静谧治愈 …);
//   - "id:<platform>": casting entry for this exact platform id.
