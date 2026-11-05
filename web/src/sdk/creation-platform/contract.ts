/**
 * 平台内部创作合同（版本 1），与 backend/internal/creationplatform/contract.go 一一对应。
 * 本目录只依赖基础类型与可注入的传输层，不依赖 React、Zustand、AntD、画布类型、window 或配置 store。
 */

export const CONTRACT_VERSION = 1;

export type Capability = "text" | "image" | "video" | "audio";

/** 逻辑模型与系统渠道模型二选一；自定义渠道不在首版合同内。 */
export type ModelSelection = { logicalModelId: string } | { channelId: string; modelKey: string };

export type ReferenceResource = {
    resourceId: string;
    kind: "image" | "video" | "audio";
    role?: string;
};

/** 结果归属，不是授权凭证。 */
export type BusinessRef = {
    application: string;
    projectId?: string;
    objectType?: string;
    objectId?: string;
    operationId?: string;
};

export type CostAuthorization = {
    maxMicrocredits?: number;
    quoteHash?: string;
};

export type GenerationRequest = {
    contractVersion: number;
    capability: Capability;
    operation?: string;
    model: ModelSelection;
    prompt: string;
    parameters?: Record<string, unknown>;
    references?: ReferenceResource[];
    business: BusinessRef;
    cost?: CostAuthorization;
    idempotencyKey: string;
    traceId?: string;
};

export type Quote = {
    capability: Capability;
    modelLabel: string;
    billingMode: string;
    quantity: number;
    amountMicrocredits: number;
    estimated: boolean;
    options?: Record<string, unknown>;
    quoteHash: string;
    expiresAt: string;
};

export type ExecutionState = "queued" | "running" | "succeeded" | "failed" | "cancelled" | "uncertain";
export type ResourceState = "none" | "delivering" | "ready" | "failed";
export type BillingState = "free" | "reserved" | "running" | "settled" | "refunded" | "pending_review";
export type Action = "cancel" | "retry" | "recover_media";

/** chargedMicrocredits 只在 settled 时出现；缺失表示未知，不能当作 0。 */
export type BillingSummary = {
    status: BillingState;
    reservedMicrocredits: number;
    chargedMicrocredits?: number;
    refundedMicrocredits: number;
    authorizedMaxMicrocredits?: number;
};

export type ResourceRef = {
    resourceId: string;
    kind: string;
    mimeType?: string;
    bytes?: number;
    width?: number;
    height?: number;
    durationMs?: number;
};

export type GenerationResult = {
    text?: string;
    resources?: ResourceRef[];
};

export type PlatformErrorCode =
    | "invalid_request"
    | "unauthorized"
    | "forbidden"
    | "not_found"
    | "model_unavailable"
    | "model_not_supported"
    | "reference_unavailable"
    | "insufficient_credits"
    | "cost_limit_exceeded"
    | "quote_expired"
    | "idempotency_conflict"
    | "active_task_limit"
    | "action_not_allowed"
    | "billing_pending_review"
    | "media_delivery_failed"
    | "unavailable"
    | "internal"
    | "transport";

export type PlatformError = {
    code: PlatformErrorCode;
    reason: string;
    message: string;
    retryable: boolean;
    taskId?: string;
    requestId?: string;
    httpStatus?: number;
};

export type TaskSnapshot = {
    contractVersion: number;
    taskId: string;
    revision: number;
    capability: Capability;
    execution: ExecutionState;
    stage: string;
    progress: number;
    resources: ResourceState;
    billing: BillingSummary;
    actions: Action[];
    business: BusinessRef;
    result?: GenerationResult;
    error?: PlatformError;
    createdAt: string;
    updatedAt: string;
};

export type SubmitReceipt = {
    snapshot: TaskSnapshot;
    replayed: boolean;
};

export type EventType = "task.submitted" | "task.state_changed" | "task.billing_changed";

export type TaskEvent = {
    id: number;
    taskId: string;
    revision: number;
    type: EventType;
    execution: ExecutionState;
    resources: ResourceState;
    billing: BillingState;
    occurredAt: string;
};

export type EventPage = {
    events: TaskEvent[];
    nextCursor: string;
    resyncRequired?: boolean;
};

export type EventQuery = {
    taskId?: string;
    application?: string;
    projectId?: string;
    after?: string;
    limit?: number;
};

export type BillingAggregate = {
    application: string;
    projectId?: string;
    taskCount: number;
    reservedMicrocredits: number;
    chargedMicrocredits: number;
    refundedMicrocredits: number;
    pendingReviewCount: number;
};

export type CreditBalance = {
    availableMicrocredits: number;
    reservedMicrocredits: number;
};

export type ModelOption = {
    selection: ModelSelection;
    capability: Capability;
    label: string;
    billingMode?: string;
    available: boolean;
};

export type ModelCatalog = { models: ModelOption[] };

const CAPABILITIES: readonly Capability[] = ["text", "image", "video", "audio"];
const REFERENCE_KINDS = ["image", "video", "audio"];
const MAX_IDEMPOTENCY_KEY_LENGTH = 160;

const trimmed = (value: unknown) => (typeof value === "string" ? value.trim() : "");

function invalid(message: string): PlatformError {
    return { code: "invalid_request", reason: "invalid_argument", message, retryable: false };
}

/** 与 Go 侧 GenerationRequest.Validate 相同的结构校验；平台状态相关的校验仍在服务端。 */
export function validateGenerationRequest(request: GenerationRequest): PlatformError | null {
    if (request.contractVersion !== CONTRACT_VERSION) return invalid("不支持的合同版本");
    if (!CAPABILITIES.includes(request.capability)) return invalid("生成能力无效");
    const model = (request.model ?? {}) as Record<string, unknown>;
    const logical = trimmed(model.logicalModelId) !== "";
    const channel = trimmed(model.channelId) !== "";
    const modelKey = trimmed(model.modelKey) !== "";
    if (logical && (channel || modelKey)) return invalid("逻辑模型与系统渠道模型只能选择一种");
    if (!logical && !(channel && modelKey)) return invalid("必须选择逻辑模型，或同时指定系统渠道与模型");
    if (trimmed(request.prompt) === "") return invalid("提示词不能为空");
    const key = trimmed(request.idempotencyKey);
    if (key === "" || key.length > MAX_IDEMPOTENCY_KEY_LENGTH) return invalid("幂等键必须为 1 到 160 个字符");
    if (trimmed(request.business?.application) === "") return invalid("业务归属缺少 application");
    if (request.cost?.maxMicrocredits !== undefined && request.cost.maxMicrocredits < 0) return invalid("费用上限不能为负数");
    for (const reference of request.references ?? []) {
        if (trimmed(reference.resourceId) === "") return invalid("参考素材缺少资源 ID");
        if (!REFERENCE_KINDS.includes(reference.kind)) return invalid("参考素材类型无效");
    }
    return null;
}

export function allowsAction(snapshot: Pick<TaskSnapshot, "actions">, action: Action) {
    return snapshot.actions.includes(action);
}

export function isTerminalExecution(state: ExecutionState) {
    return state === "succeeded" || state === "failed" || state === "cancelled";
}
