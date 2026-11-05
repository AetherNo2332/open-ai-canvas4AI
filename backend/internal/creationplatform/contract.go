// Package creationplatform 定义平台内部创作能力（模型生成、任务、计费、媒体资源）的合同。
//
// 合同只描述业务可见的意图与事实：模型协议、上游凭据、价格计算和资源持久化都由平台在
// internal/app 组合根内解析，不出现在这里。本包不导入 app、service、repository 或 model，
// 业务适配层（短剧、电商、画布）只依赖这些类型与 ports.go 中的端口。
package creationplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// ContractVersion 是当前唯一支持的合同版本；请求必须显式声明。
const ContractVersion = 1

// Capability 是首版支持的四种生成能力。
type Capability string

const (
	CapabilityText  Capability = "text"
	CapabilityImage Capability = "image"
	CapabilityVideo Capability = "video"
	CapabilityAudio Capability = "audio"
)

func (c Capability) Valid() bool {
	switch c {
	case CapabilityText, CapabilityImage, CapabilityVideo, CapabilityAudio:
		return true
	}
	return false
}

// ModelSelection 是逻辑模型与系统渠道模型的联合类型：恰好选择一条路线。
// 自定义渠道不在首版合同内（决策 D1），继续走原有 /api/ai/custom 与任务入口。
type ModelSelection struct {
	LogicalModelID string `json:"logicalModelId,omitempty"`
	ChannelID      string `json:"channelId,omitempty"`
	ModelKey       string `json:"modelKey,omitempty"`
}

// ReferenceResource 只按稳定资源 ID 引用参考素材；临时 URL 不能作为身份。
type ReferenceResource struct {
	ResourceID string `json:"resourceId"`
	Kind       string `json:"kind"`
	// Role 表达素材用途（reference、first_frame、last_frame、mask），由平台映射到协议字段。
	Role string `json:"role,omitempty"`
}

// BusinessRef 记录结果归属，供汇总与追踪；它不是授权凭证，归属由适配器验证。
type BusinessRef struct {
	Application string `json:"application"`
	ProjectID   string `json:"projectId,omitempty"`
	ObjectType  string `json:"objectType,omitempty"`
	ObjectID    string `json:"objectId,omitempty"`
	OperationID string `json:"operationId,omitempty"`
}

// CostAuthorization 是单次费用授权（决策 D2）：上限为空表示按报价冻结、不额外设限。
type CostAuthorization struct {
	MaxMicrocredits *int64 `json:"maxMicrocredits,omitempty"`
	// QuoteHash 来自 Quote；提交时价格或规格已变化则返回 quote_expired，而不是静默按新价扣费。
	QuoteHash string `json:"quoteHash,omitempty"`
}

// GenerationRequest 是所有业务共用的提交合同。
type GenerationRequest struct {
	ContractVersion int                 `json:"contractVersion"`
	Capability      Capability          `json:"capability"`
	Operation       string              `json:"operation,omitempty"`
	Model           ModelSelection      `json:"model"`
	Prompt          string              `json:"prompt"`
	Parameters      map[string]any      `json:"parameters,omitempty"`
	References      []ReferenceResource `json:"references,omitempty"`
	Business        BusinessRef         `json:"business"`
	Cost            CostAuthorization   `json:"cost,omitempty"`
	IdempotencyKey  string              `json:"idempotencyKey"`
	// TraceID 只用于链路追踪，不参与幂等指纹。
	TraceID string `json:"traceId,omitempty"`
}

const maxIdempotencyKeyLength = 160

// Validate 只做与平台状态无关的结构校验；模型、权限、资源归属与价格在平台内校验。
func (r GenerationRequest) Validate() error {
	invalid := func(message string) error { return NewError(CodeInvalidRequest, message) }
	if r.ContractVersion != ContractVersion {
		return invalid("不支持的合同版本")
	}
	if !r.Capability.Valid() {
		return invalid("生成能力无效")
	}
	logical := strings.TrimSpace(r.Model.LogicalModelID) != ""
	channel, modelKey := strings.TrimSpace(r.Model.ChannelID) != "", strings.TrimSpace(r.Model.ModelKey) != ""
	switch {
	case logical && (channel || modelKey):
		return invalid("逻辑模型与系统渠道模型只能选择一种")
	case !logical && !(channel && modelKey):
		return invalid("必须选择逻辑模型，或同时指定系统渠道与模型")
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return invalid("提示词不能为空")
	}
	key := strings.TrimSpace(r.IdempotencyKey)
	if key == "" || len(key) > maxIdempotencyKeyLength {
		return invalid("幂等键必须为 1 到 160 个字符")
	}
	if strings.TrimSpace(r.Business.Application) == "" {
		return invalid("业务归属缺少 application")
	}
	if r.Cost.MaxMicrocredits != nil && *r.Cost.MaxMicrocredits < 0 {
		return invalid("费用上限不能为负数")
	}
	for _, ref := range r.References {
		if strings.TrimSpace(ref.ResourceID) == "" {
			return invalid("参考素材缺少资源 ID")
		}
		switch ref.Kind {
		case "image", "video", "audio":
		default:
			return invalid("参考素材类型无效")
		}
	}
	return nil
}

// Fingerprint 是幂等比对用的内容摘要：同键同指纹返回原任务，同键异指纹返回冲突。
// encoding/json 对 map 键排序，参数顺序不影响结果；TraceID 不参与。
func (r GenerationRequest) Fingerprint() string {
	stable := r
	stable.TraceID = ""
	stable.IdempotencyKey = strings.TrimSpace(stable.IdempotencyKey)
	stable.Prompt = strings.TrimSpace(stable.Prompt)
	encoded, _ := json.Marshal(stable)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (r GenerationRequest) clone() GenerationRequest {
	encoded, _ := json.Marshal(r)
	var copied GenerationRequest
	_ = json.Unmarshal(encoded, &copied)
	copied.TraceID = r.TraceID
	return copied
}

// Quote 是不冻结、不派发的报价。金额为整数微积分；Estimated 表示按 token 预估。
type Quote struct {
	Capability         Capability     `json:"capability"`
	ModelLabel         string         `json:"modelLabel"`
	BillingMode        string         `json:"billingMode"`
	Quantity           int64          `json:"quantity"`
	AmountMicrocredits int64          `json:"amountMicrocredits"`
	Estimated          bool           `json:"estimated"`
	Options            map[string]any `json:"options,omitempty"`
	QuoteHash          string         `json:"quoteHash"`
	ExpiresAt          time.Time      `json:"expiresAt"`
}

// ExecutionState、ResourceState 与 BillingState 分别表达执行、交付和费用，三者互不推导。
type ExecutionState string

const (
	ExecutionQueued    ExecutionState = "queued"
	ExecutionRunning   ExecutionState = "running"
	ExecutionSucceeded ExecutionState = "succeeded"
	ExecutionFailed    ExecutionState = "failed"
	ExecutionCancelled ExecutionState = "cancelled"
	// ExecutionUncertain 表示上游是否接单未知，不能当作失败重提。
	ExecutionUncertain ExecutionState = "uncertain"
)

func (s ExecutionState) Terminal() bool {
	return s == ExecutionSucceeded || s == ExecutionFailed || s == ExecutionCancelled
}

type ResourceState string

const (
	ResourcesNone       ResourceState = "none"
	ResourcesDelivering ResourceState = "delivering"
	ResourcesReady      ResourceState = "ready"
	ResourcesFailed     ResourceState = "failed"
)

type BillingState string

const (
	BillingFree          BillingState = "free"
	BillingReserved      BillingState = "reserved"
	BillingRunning       BillingState = "running"
	BillingSettled       BillingState = "settled"
	BillingRefunded      BillingState = "refunded"
	BillingPendingReview BillingState = "pending_review"
)

// BillingSummary 区分冻结与实收：ChargedMicrocredits 只在 settled 时给出，未知不填 0。
type BillingSummary struct {
	Status                    BillingState `json:"status"`
	ReservedMicrocredits      int64        `json:"reservedMicrocredits"`
	ChargedMicrocredits       *int64       `json:"chargedMicrocredits,omitempty"`
	RefundedMicrocredits      int64        `json:"refundedMicrocredits"`
	AuthorizedMaxMicrocredits *int64       `json:"authorizedMaxMicrocredits,omitempty"`
}

// Action 是服务端当前允许的操作；SDK 不自行推断是否可重试或退款。
type Action string

const (
	ActionCancel       Action = "cancel"
	ActionRetry        Action = "retry"
	ActionRecoverMedia Action = "recover_media"
)

// ResourceRef 是生成结果中的资源事实；访问地址另经授权接口获取。
type ResourceRef struct {
	ResourceID string `json:"resourceId"`
	Kind       string `json:"kind"`
	MimeType   string `json:"mimeType,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

// GenerationResult 是最终文本或资源列表，不含上游凭据和受保护配置。
type GenerationResult struct {
	Text      string        `json:"text,omitempty"`
	Resources []ResourceRef `json:"resources,omitempty"`
}

// TaskSnapshot 是可随时重新读取的完整状态；Revision 单调递增，供事件消费者丢弃旧状态。
type TaskSnapshot struct {
	ContractVersion int               `json:"contractVersion"`
	TaskID          string            `json:"taskId"`
	Revision        int64             `json:"revision"`
	Capability      Capability        `json:"capability"`
	Execution       ExecutionState    `json:"execution"`
	Stage           string            `json:"stage"`
	Progress        int               `json:"progress"`
	Resources       ResourceState     `json:"resources"`
	Billing         BillingSummary    `json:"billing"`
	Actions         []Action          `json:"actions"`
	Business        BusinessRef       `json:"business"`
	Result          *GenerationResult `json:"result,omitempty"`
	Error           *Error            `json:"error,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

func (s TaskSnapshot) Allows(action Action) bool {
	for _, allowed := range s.Actions {
		if allowed == action {
			return true
		}
	}
	return false
}

// SubmitReceipt 是提交结果；Replayed 表示命中既有幂等记录、没有创建新任务。
type SubmitReceipt struct {
	Snapshot TaskSnapshot `json:"snapshot"`
	Replayed bool         `json:"replayed"`
}

// EventType 是持久化任务事件类型。
type EventType string

const (
	EventSubmitted    EventType = "task.submitted"
	EventStateChanged EventType = "task.state_changed"
	EventBilling      EventType = "task.billing_changed"
)

// TaskEvent 只带最小状态；消费者按 ID 去重，需要完整信息时重新读取快照。
type TaskEvent struct {
	ID         int64          `json:"id"`
	TaskID     string         `json:"taskId"`
	Revision   int64          `json:"revision"`
	Type       EventType      `json:"type"`
	Execution  ExecutionState `json:"execution"`
	Resources  ResourceState  `json:"resources"`
	Billing    BillingState   `json:"billing"`
	OccurredAt time.Time      `json:"occurredAt"`
}

// EventPage 是按游标续读的一页事件。ResyncRequired 表示游标已过期，需先读快照再从 NextCursor 继续。
type EventPage struct {
	Events         []TaskEvent `json:"events"`
	NextCursor     string      `json:"nextCursor"`
	ResyncRequired bool        `json:"resyncRequired,omitempty"`
}

// EventQuery 按任务或业务归属读取事件，二者至少给一个。
type EventQuery struct {
	TaskID      string `json:"taskId,omitempty"`
	Application string `json:"application,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	After       string `json:"after,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// BillingAggregate 是按业务归属汇总的费用；冻结、实收、退款与待核对分列。
type BillingAggregate struct {
	Application          string `json:"application"`
	ProjectID            string `json:"projectId,omitempty"`
	TaskCount            int64  `json:"taskCount"`
	ReservedMicrocredits int64  `json:"reservedMicrocredits"`
	ChargedMicrocredits  int64  `json:"chargedMicrocredits"`
	RefundedMicrocredits int64  `json:"refundedMicrocredits"`
	PendingReviewCount   int64  `json:"pendingReviewCount"`
}

// CreditBalance 是当前可用与冻结额度。
type CreditBalance struct {
	AvailableMicrocredits int64 `json:"availableMicrocredits"`
	ReservedMicrocredits  int64 `json:"reservedMicrocredits"`
}

// ModelOption 是可提交的一个模型选择及其能力摘要。
type ModelOption struct {
	Selection   ModelSelection `json:"selection"`
	Capability  Capability     `json:"capability"`
	Label       string         `json:"label"`
	BillingMode string         `json:"billingMode,omitempty"`
	Available   bool           `json:"available"`
}

type ModelCatalog struct {
	Models []ModelOption `json:"models"`
}
