import type { AgentApprovalDecision, AgentApprovalSnapshot, AgentBusinessScope, AgentConversation, AgentConversationQueue, AgentEvent, AgentMessageInput, AgentQueuedMessage, AgentRunRequest, AgentRunSnapshot } from "@/sdk/agent";
import type { createAgentClient } from "@/sdk/agent/client";
import { defaultAgentConversationStore, type AgentConversationStore } from "./conversation-storage";
import type { AgentConversationDocument, AgentConversationIdentity, AgentConversationRecord, AgentConversationView, AgentPendingSubmission } from "./conversation-types";
import { applyAgentConversationEvent, projectAgentConversation } from "@/lib/agent/conversation-events";
import { emptyAgentContextUsage, reduceAgentContextUsage, type AgentContextUsage } from "@/lib/agent/context-usage";

type ScopedClient = ReturnType<typeof createAgentClient>;

export type AgentConversationControllerState = {
    status: "loading" | "ready" | "error";
    error: string;
    busy: boolean;
    connection: "connecting" | "connected" | "reconnecting" | "disconnected";
    conversations: AgentConversationRecord[];
    activeConversationId: string;
    messages: AgentConversationRecord["messages"];
    run: AgentRunSnapshot | null;
    approval: AgentApprovalSnapshot | null;
    queue?: AgentConversationQueue;
    pending?: AgentPendingSubmission | null;
    draft: string;
    contextUsage: AgentContextUsage;
    model: string;
    reasoningMode: NonNullable<AgentRunRequest["reasoningMode"]>;
    skillIds: string[];
    sendMode: AgentMessageInput["mode"];
};

export type AgentConversationSubmitOptions = {
    request: AgentRunRequest;
    mode?: AgentMessageInput["mode"];
    replyToQuestionId?: string;
    preserveDraft?: boolean;
};

type ControllerOptions = {
    identity: AgentConversationIdentity;
    client: ScopedClient;
    store?: AgentConversationStore;
    prepare?: () => Promise<void>;
    createId?: () => string;
    now?: () => string;
    initial?: Partial<Pick<AgentConversationControllerState, "model" | "reasoningMode" | "skillIds" | "sendMode">>;
};

function activeRun(run: AgentRunSnapshot | null | undefined) {
    return Boolean(run && (run.cleanupPending || ["queued", "running", "waiting_approval"].includes(run.status)));
}

function sameScope(left: AgentBusinessScope, right: AgentBusinessScope) {
    return left.type === right.type && left.id === right.id;
}

function recordFromConversation(summary: AgentConversation, existing?: AgentConversationRecord): AgentConversationRecord {
    const record = existing || { id: summary.id, messages: [], run: null, permissionMode: "read_only" as const, createdAt: summary.createdAt };
    return { ...record, serverConversationId: summary.id, title: summary.title, archived: summary.archived, updatedAt: summary.updatedAt };
}

export class AgentConversationController {
    readonly identityKey: string;
    private readonly identity: AgentConversationIdentity;
    private readonly client: ScopedClient;
    private readonly store: AgentConversationStore;
    private readonly prepare?: () => Promise<void>;
    private readonly createId: () => string;
    private readonly now: () => string;
    private listeners = new Set<() => void>();
    private eventListeners = new Set<(event: AgentEvent) => void>();
    private stateValue: AgentConversationControllerState;
    private document: AgentConversationDocument = { version: 2, activeId: null, conversations: [] };
    private readonly lifetime = new AbortController();
    private eventController: AbortController | null = null;
    private disposed = false;
    private viewVersion = 0;
    private historyVersion = 0;
    private directoryVersion = 0;
    private runVersion = 0;
    private directoryRequest?: Promise<void>;
    private submissions = new Map<string, AgentPendingSubmission>();
    private persistTimer: ReturnType<typeof setTimeout> | undefined;

    constructor(options: ControllerOptions) {
        this.identity = {
            ...options.identity,
            scope: { ...options.identity.scope },
        };
        this.identityKey = `${this.identity.userScope}:${this.identity.scope.type}:${this.identity.scope.id}:${this.identity.agentDefinitionId}`;
        this.client = options.client;
        this.store = options.store || defaultAgentConversationStore;
        this.prepare = options.prepare;
        this.createId = options.createId || (() => globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`);
        this.now = options.now || (() => new Date().toISOString());
        const initial: Partial<Pick<AgentConversationControllerState, "model" | "reasoningMode" | "skillIds" | "sendMode">> = options.initial || {};
        this.stateValue = {
            status: "loading",
            error: "",
            busy: false,
            connection: "connecting",
            conversations: [],
            activeConversationId: "",
            messages: [],
            run: null,
            approval: null,
            draft: "",
            contextUsage: emptyAgentContextUsage(""),
            model: initial.model || "",
            reasoningMode: initial.reasoningMode || "off",
            skillIds: initial.skillIds || [],
            sendMode: initial.sendMode || "queue",
        };
    }

    subscribe = (listener: () => void) => {
        this.listeners.add(listener);
        return () => {
            this.listeners.delete(listener);
        };
    };

    subscribeEvent = (listener: (event: AgentEvent) => void) => {
        this.eventListeners.add(listener);
        return () => {
            this.eventListeners.delete(listener);
        };
    };

    getSnapshot = () => this.stateValue;

    async hydrate() {
        if (this.disposed) return;
        this.set({ status: "loading", error: "" });
        try {
            const document = await this.store.loadDocument(this.identity);
            if (this.disposed) return;
            this.document = document;
            const active = document.conversations.find((record) => record.id === document.activeId) || document.conversations[0];
            const activeConversationId = active?.id || this.createId();
            const { draft, pending } = active ? await this.loadComposer(active.id) : { draft: "", pending: null };
            if (this.disposed) return;
            this.set({
                status: "ready",
                activeConversationId,
                messages: active?.messages || [],
                run: active?.run || null,
                approval: active?.run?.approval || null,
                conversations: document.conversations,
                draft: draft ?? active?.draft ?? "",
                pending,
                contextUsage: active?.contextUsage || emptyAgentContextUsage(active?.run?.id || ""),
                model: active?.model || active?.run?.model || this.stateValue.model,
                reasoningMode: active?.reasoningMode || this.stateValue.reasoningMode,
                skillIds: active?.skillIds || this.stateValue.skillIds,
                sendMode: active?.sendMode || this.stateValue.sendMode,
            });
            const view = this.viewVersion;
            if (!active) await this.ensureActiveRecord();
            const serverId = active?.serverConversationId || active?.run?.conversationId;
            if (serverId) await this.loadHistory(activeConversationId, serverId, view);
            else await this.refreshDirectory();
        } catch (cause) {
            if (!this.disposed) this.set({ status: "error", error: cause instanceof Error ? cause.message : "Agent 会话恢复失败" });
        }
    }

    refreshDirectory() {
        if (this.disposed || this.stateValue.status !== "ready") return Promise.resolve();
        if (!this.directoryRequest) {
            this.directoryRequest = this.readDirectory().finally(() => {
                this.directoryRequest = undefined;
            });
        }
        return this.directoryRequest;
    }

    async reconnect() {
        const id = this.stateValue.activeConversationId;
        const view = this.viewVersion;
        const runId = this.stateValue.run?.id;
        await this.refreshDirectory();
        const run = this.stateValue.run;
        if (this.isCurrentView(id, view) && run && run.id === runId && this.stateValue.connection === "disconnected" && activeRun(run)) this.connectRun(run);
    }

    private async readDirectory() {
        const version = ++this.directoryVersion;
        const runVersion = this.runVersion;
        const view = this.viewVersion;
        const localId = this.stateValue.activeConversationId;
        try {
            const items = new Map<string, AgentConversation>();
            let before: string | undefined;
            do {
                const page = await this.client.listConversations({ before, limit: 100 }, this.signal());
                if (this.disposed || version !== this.directoryVersion || runVersion !== this.runVersion) return;
                for (const summary of page.items) items.set(summary.id, summary);
                before = page.nextCursor;
            } while (before);
            const conversations = this.document.conversations.map((record) => {
                const summary = items.get(record.serverConversationId || record.id);
                return summary ? recordFromConversation(summary, record) : record;
            });
            const known = new Set(conversations.map((record) => record.serverConversationId || record.id));
            for (const summary of items.values()) {
                if (!known.has(summary.id)) conversations.push(recordFromConversation(summary));
            }
            this.document = { version: 2, activeId: this.stateValue.activeConversationId || null, conversations };
            this.set({ conversations });
            this.schedulePersist();
            if (!this.isCurrentView(localId, view) || this.submissions.has(localId)) return;
            const active = this.record(localId);
            const serverId = active?.serverConversationId;
            const latest = serverId ? items.get(serverId)?.latestRunId : undefined;
            if (serverId && latest && (this.stateValue.run?.id !== latest || active?.historyState?.status === "unavailable")) {
                await this.loadHistory(localId, serverId, view);
            }
        } catch (cause) {
            if (this.isCurrentView(localId, view) && version === this.directoryVersion && runVersion === this.runVersion) this.set({ error: cause instanceof Error ? cause.message : "Agent 目录读取失败" });
        }
    }

    async openConversation(id: string) {
        if (this.disposed || !this.record(id)) return;
        const view = ++this.viewVersion;
        let composer: Awaited<ReturnType<AgentConversationController["loadComposer"]>>;
        try {
            composer = await this.loadComposer(id);
        } catch (cause) {
            if (!this.disposed && view === this.viewVersion) this.set({ error: cause instanceof Error ? cause.message : "Agent 草稿读取失败" });
            return;
        }
        const { draft, pending } = composer;
        const record = this.record(id);
        if (this.disposed || view !== this.viewVersion || !record) return;
        this.stopEvents();
        this.document = { ...this.document, activeId: id };
        this.set({
            activeConversationId: id,
            messages: record.messages,
            run: record.run,
            approval: record.run?.approval || null,
            draft: draft ?? record.draft ?? "",
            queue: undefined,
            pending: this.submissions.get(id) || pending,
            busy: this.submissions.has(id),
            connection: "connected",
            error: "",
            contextUsage: record.contextUsage || emptyAgentContextUsage(record.run?.id || ""),
            model: record.model || record.run?.model || "",
            reasoningMode: record.reasoningMode || "off",
            skillIds: record.skillIds || [],
            sendMode: record.sendMode || this.stateValue.sendMode,
        });
        const serverId = record.serverConversationId || record.run?.conversationId;
        if (serverId) await this.loadHistory(id, serverId, view);
        this.schedulePersist();
    }

    async openRun(runId: string) {
        if (this.disposed || !runId) return false;
        const view = ++this.viewVersion;
        const { run } = await this.client.getRun(runId, this.signal(), { eventLimit: 1 });
        if (this.disposed || view !== this.viewVersion) return false;
        if (run.scope && !sameScope(run.scope, this.identity.scope)) {
            throw new Error("该运行不属于当前页面范围");
        }
        const conversationId = run.conversationId || run.id;
        const existing = this.document.conversations.find((item) => item.serverConversationId === conversationId || item.run?.conversationId === conversationId);
        const recordId = existing?.id || conversationId;
        const { draft, pending } = await this.loadComposer(recordId);
        if (this.disposed || view !== this.viewVersion) return false;
        this.stopEvents();
        const current = this.record(recordId);
        const record: AgentConversationRecord = existing
            ? { ...(current || existing), run }
            : {
                  id: recordId,
                  serverConversationId: conversationId,
                  title: run.userPrompt || "历史对话",
                  messages: [],
                  run,
                  permissionMode: run.permissionMode,
                  createdAt: run.createdAt,
                  updatedAt: run.updatedAt,
              };
        this.document = {
            version: 2,
            activeId: recordId,
            conversations: existing ? this.document.conversations.map((item) => (item.id === recordId ? record : item)) : [record, ...this.document.conversations],
        };
        this.set({
            activeConversationId: recordId,
            messages: record.messages,
            run,
            approval: run.approval || null,
            draft: draft ?? record.draft ?? "",
            queue: undefined,
            pending: this.submissions.get(recordId) || pending,
            busy: this.submissions.has(recordId),
            connection: "connected",
            error: "",
            conversations: this.document.conversations,
            contextUsage: record.contextUsage || emptyAgentContextUsage(run.id),
            model: record.model || run.model || "",
            reasoningMode: record.reasoningMode || "off",
            skillIds: record.skillIds || run.skills?.map((skill) => skill.id) || [],
            sendMode: record.sendMode || this.stateValue.sendMode,
        });
        await this.store.saveDocument(this.identity, this.document);
        await this.loadHistory(recordId, conversationId, view);
        return true;
    }

    async newConversation() {
        if (this.disposed || this.stateValue.status !== "ready") return;
        ++this.viewVersion;
        this.stopEvents();
        const id = this.createId();
        const record: AgentConversationRecord = {
            id,
            title: "新对话",
            messages: [],
            run: null,
            permissionMode: "read_only",
            model: this.stateValue.model || undefined,
            reasoningMode: this.stateValue.reasoningMode,
            skillIds: [],
            contextUsage: emptyAgentContextUsage(""),
            sendMode: this.stateValue.sendMode,
            createdAt: this.now(),
            updatedAt: this.now(),
        };
        this.document = { version: 2, activeId: id, conversations: [record, ...this.document.conversations] };
        this.set({
            activeConversationId: id,
            messages: [],
            run: null,
            approval: null,
            queue: undefined,
            pending: null,
            draft: "",
            error: "",
            busy: false,
            connection: "connected",
            contextUsage: emptyAgentContextUsage(""),
            model: this.stateValue.model,
            reasoningMode: this.stateValue.reasoningMode,
            skillIds: [],
            sendMode: this.stateValue.sendMode,
        });
        await this.store.clearDraft(this.identity, id);
        await this.store.saveDocument(this.identity, this.document);
    }

    setDraft(draft: string) {
        if (this.disposed || !this.stateValue.activeConversationId) return;
        this.set({ draft });
        this.updateRecord({ draft });
        void this.store.saveDraft(this.identity, this.stateValue.activeConversationId, draft);
        this.schedulePersist();
    }

    setModel(model: string) {
        if (this.stateValue.model === model) return;
        this.set({ model });
        this.updateRecord({ model });
        this.schedulePersist();
    }

    setReasoningMode(reasoningMode: NonNullable<AgentRunRequest["reasoningMode"]>) {
        if (this.stateValue.reasoningMode === reasoningMode) return;
        this.set({ reasoningMode });
        this.updateRecord({ reasoningMode });
        this.schedulePersist();
    }

    setSkillIds(skillIds: string[]) {
        const next = [...new Set(skillIds.filter(Boolean))];
        if (JSON.stringify(this.stateValue.skillIds) === JSON.stringify(next)) return;
        this.set({ skillIds: next });
        this.updateRecord({ skillIds: next });
        this.schedulePersist();
    }

    setSendMode(sendMode: AgentMessageInput["mode"]) {
        if (this.stateValue.sendMode === sendMode) return;
        this.set({ sendMode });
        this.updateRecord({ sendMode });
        this.schedulePersist();
    }

    async submit(options: AgentConversationSubmitOptions) {
        const localId = this.stateValue.activeConversationId;
        if (this.disposed || this.stateValue.status !== "ready" || !localId || this.submissions.has(localId)) return false;
        const request = this.normalizeRequest(options.request);
        const conversationId = this.currentServerConversationId();
        const previous = this.stateValue.pending;
        if (previous?.request) {
            if (previous.request.prompt !== request.prompt || previous.request.idempotencyKey !== request.idempotencyKey) {
                this.set({ error: "上一条请求结果待确认，请先原样重试上一条消息，再发送新要求" });
                return false;
            }
        }
        const parentRunId = this.stateValue.run?.id;
        const conversationMessage =
            this.stateValue.run && conversationId
                ? {
                      conversationId,
                      mode: options.mode || (this.stateValue.queue?.awaitingQuestionId ? "interject" : this.stateValue.sendMode),
                      expectedRunId: this.stateValue.run.id,
                      ...(options.replyToQuestionId ? { replyToQuestionId: options.replyToQuestionId } : {}),
                  }
                : undefined;
        const pending: AgentPendingSubmission = previous?.request
            ? previous
            : {
                  fingerprint: JSON.stringify({ scope: this.identity.scope, parent: parentRunId, request, conversationMessage }),
                  key: request.idempotencyKey,
                  request,
                  ...(parentRunId ? { parentRunId } : {}),
                  messageId: `user-${request.idempotencyKey}`,
                  ...(!options.preserveDraft ? { composerDraft: this.stateValue.draft } : {}),
                  ...(conversationMessage ? { conversationMessage } : {}),
              };
        this.submissions.set(localId, pending);
        this.set({ busy: true, error: "" });
        try {
            await this.prepare?.();
            if (this.disposed) return false;
            const record = this.record(localId);
            if (!record) return false;
            const messages =
                options.preserveDraft || record.messages.some((message) => message.id === pending.messageId) ? record.messages : [...record.messages, { id: pending.messageId || `user-${pending.key}`, role: "user" as const, text: request.prompt }];
            await this.store.savePending(this.identity, localId, pending);
            if (this.disposed) return false;
            this.patchRecord(localId, { messages });
            if (this.isCurrentView(localId)) this.set({ pending, messages });
            await this.store.saveDocument(this.identity, this.document);
            let nextRun: AgentRunSnapshot | undefined;
            let conversationDelta: AgentConversationQueue | undefined;
            let receiptConversationId = "";
            if (pending.conversationMessage) {
                const { conversationId: target, ...message } = pending.conversationMessage;
                const receipt = await this.client.sendMessage(target, { ...message, request: pending.request! }, this.signal());
                receiptConversationId = receipt.conversationId || target;
                conversationDelta = await this.client.getConversationQueue(target, this.signal());
                if (receipt.runId) nextRun = (await this.client.getRun(receipt.runId, this.signal(), { fromStart: true, eventLimit: 500 })).run;
            } else if (pending.parentRunId) {
                nextRun = (await this.client.continueRun(pending.parentRunId, pending.request!, this.signal())).run;
            } else {
                nextRun = (await this.client.startRun(pending.request!, this.signal())).run;
            }
            if (this.disposed) return false;
            ++this.runVersion;
            await this.settleSubmission(localId, pending, nextRun, conversationDelta, receiptConversationId, Boolean(options.preserveDraft));
            return true;
        } catch (cause) {
            if (this.disposed) return false;
            const status = (cause as { status?: number }).status;
            const reason = (cause as { reason?: string }).reason || "";
            const definitive = Boolean(status && [400, 401, 403, 404, 422].includes(status)) || ["agent_conversation_stale_parent", "agent_question_pending", "agent_question_answer_pending", "agent_question_stale"].includes(reason);
            if (definitive) {
                await this.store.clearPending(this.identity, localId).catch(() => undefined);
                if (this.isCurrentView(localId)) this.set({ pending: null });
            }
            if (this.isCurrentView(localId)) this.set({ error: cause instanceof Error ? cause.message : "Agent 请求失败，可重试原消息" });
            return false;
        } finally {
            this.submissions.delete(localId);
            if (this.isCurrentView(localId)) this.set({ busy: false });
        }
    }

    private async settleSubmission(localId: string, pending: AgentPendingSubmission, run: AgentRunSnapshot | undefined, queue: AgentConversationQueue | undefined, receiptConversationId: string, preserveDraft: boolean) {
        const record = this.record(localId);
        if (!record) return;
        const serverConversationId = run?.conversationId || receiptConversationId;
        const clearDraft = !preserveDraft && (record.draft ?? "") === pending.composerDraft;
        const messages = run ? record.messages.map((message) => (message.id === pending.messageId ? { ...message, id: `user-${run.id}`, runId: run.id } : message)) : record.messages;
        this.patchRecord(localId, {
            messages,
            ...(run ? { run, model: run.model || record.model } : {}),
            ...(serverConversationId ? { serverConversationId } : {}),
            ...(clearDraft ? { draft: "" } : {}),
        });
        if (this.isCurrentView(localId)) {
            this.set({ messages, ...(clearDraft ? { draft: "" } : {}), ...(run ? { run, approval: run.approval || null } : {}) });
            if (queue) this.applyQueue(queue);
            if (run) this.connectRun(run);
        }
        if (clearDraft) await this.store.saveDraft(this.identity, localId, "");
        await this.store.saveDocument(this.identity, this.document);
        await this.store.clearPending(this.identity, localId);
        if (this.isCurrentView(localId)) this.set({ pending: null });
    }

    async retryPending() {
        const pending = this.stateValue.pending;
        if (!pending?.request) return false;
        return this.submit({ request: pending.request, preserveDraft: true });
    }

    async refreshQueue() {
        const conversationId = this.currentServerConversationId();
        const localId = this.stateValue.activeConversationId;
        const view = this.viewVersion;
        if (this.disposed || !conversationId) return;
        try {
            const queue = await this.client.getConversationQueue(conversationId, this.signal());
            if (this.isCurrentView(localId, view) && this.currentServerConversationId() === conversationId) this.applyQueue(queue);
        } catch (cause) {
            if (this.isCurrentView(localId, view)) this.set({ error: cause instanceof Error ? cause.message : "待发消息读取失败" });
        }
    }

    private applyQueue(queue: AgentConversationQueue) {
        const current = this.stateValue.queue;
        if (current?.conversationId === queue.conversationId && current.revision > queue.revision) return;
        this.set({ queue });
    }

    async editQueued(item: AgentQueuedMessage, prompt: string) {
        const conversationId = this.currentServerConversationId();
        const view = this.viewVersion;
        if (!conversationId) return false;
        await this.client.updateQueuedMessage(conversationId, item.id, { revision: item.revision, prompt }, this.signal());
        if (this.viewVersion === view) await this.refreshQueue();
        return true;
    }

    async interjectQueued(item: AgentQueuedMessage) {
        const conversationId = this.currentServerConversationId();
        const view = this.viewVersion;
        if (!conversationId) return false;
        await this.client.updateQueuedMessage(conversationId, item.id, { revision: item.revision, action: "interject" }, this.signal());
        if (this.viewVersion === view) await this.refreshQueue();
        return true;
    }

    async cancelQueued(item: AgentQueuedMessage) {
        const conversationId = this.currentServerConversationId();
        const view = this.viewVersion;
        if (!conversationId) return false;
        await this.client.cancelQueuedMessage(conversationId, item.id, item.revision, this.signal());
        if (this.viewVersion === view) await this.refreshQueue();
        return true;
    }

    async updateQueue(action: "pause" | "resume") {
        const conversationId = this.currentServerConversationId();
        const localId = this.stateValue.activeConversationId;
        const view = this.viewVersion;
        if (!conversationId || !this.stateValue.queue) return false;
        const queue = await this.client.updateConversationQueue(conversationId, { revision: this.stateValue.queue.revision, action }, this.signal());
        if (this.isCurrentView(localId, view)) this.applyQueue(queue);
        return true;
    }

    async cancelRun() {
        const run = this.stateValue.run;
        const view = this.viewVersion;
        if (!run) return false;
        const receipt = await this.client.cancelRun(run.id, this.signal());
        if (receipt.accepted) {
            const { run: next } = await this.client.getRun(run.id, this.signal(), { eventLimit: 1 });
            if (!this.disposed && view === this.viewVersion && this.stateValue.run?.id === run.id) {
                this.set({ run: next, approval: next.approval || null });
                this.updateRecord({ run: next });
                this.schedulePersist();
            }
        }
        return receipt.accepted;
    }

    async decideApproval(decision: "approve" | "reject", input: Omit<AgentApprovalDecision, "decision"> = {}) {
        const run = this.stateValue.run;
        const approval = this.stateValue.approval;
        const view = this.viewVersion;
        if (!run || !approval) return false;
        await this.client.decideApproval(run.id, approval.approvalId, { decision, ...input }, this.signal());
        if (!this.disposed && view === this.viewVersion && this.stateValue.run?.id === run.id && this.stateValue.approval?.approvalId === approval.approvalId) this.set({ approval: null });
        return true;
    }

    async changePermission(permissionMode: AgentRunSnapshot["permissionMode"]) {
        const run = this.stateValue.run;
        const localId = this.stateValue.activeConversationId;
        if (!run) return false;
        const receipt = await this.client.changePermission(run.id, { permissionMode, requestId: this.createId() }, this.signal());
        const current = this.record(localId)?.run;
        if (!this.disposed && receipt.changed && current?.id === run.id) {
            const next = { ...current, permissionMode: receipt.permissionMode };
            this.patchRecord(localId, { run: next });
            if (this.isCurrentView(localId) && this.stateValue.run?.id === run.id) this.set({ run: next });
            this.schedulePersist();
        }
        return receipt.changed;
    }

    async archiveConversation(id: string) {
        const record = this.document.conversations.find((item) => item.id === id);
        if (this.disposed || !record || this.submissions.has(id)) return false;
        if (record.serverConversationId) await this.client.archiveConversation(record.serverConversationId, this.signal());
        if (this.disposed) return false;
        ++this.directoryVersion;
        const conversations = this.document.conversations.filter((item) => item.id !== id);
        this.document = { ...this.document, conversations };
        this.set({ conversations });
        if (id === this.stateValue.activeConversationId) await this.newConversation();
        this.schedulePersist();
        return true;
    }

    dispose() {
        this.disposed = true;
        this.lifetime.abort();
        this.stopEvents();
        if (this.persistTimer) clearTimeout(this.persistTimer);
        this.listeners.clear();
        this.eventListeners.clear();
    }

    private signal() {
        return this.lifetime.signal;
    }

    private isCurrentView(id: string, version = this.viewVersion) {
        return !this.disposed && version === this.viewVersion && id === this.stateValue.activeConversationId;
    }

    private record(id: string) {
        return this.document.conversations.find((item) => item.id === id);
    }

    private async loadComposer(id: string) {
        const [draft, pending] = await Promise.all([this.store.loadDraft(this.identity, id), this.store.loadPending(this.identity, id)]);
        return { draft, pending };
    }

    private normalizeRequest(request: AgentRunRequest): AgentRunRequest {
        const scope = this.identity.scope;
        if (request.scope && !sameScope(request.scope, scope)) throw new Error("请求范围与当前页面不一致");
        const normalized = {
            ...request,
            scope,
            ...(request.reasoningMode ? {} : { reasoningMode: this.stateValue.reasoningMode }),
            ...(request.skillIds?.length || !this.stateValue.skillIds.length ? {} : { skillIds: [...this.stateValue.skillIds] }),
            ...(request.model || !this.stateValue.model ? {} : { model: this.stateValue.model }),
        } as AgentRunRequest;
        if (scope.type === "canvas") normalized.canvasId = scope.id;
        else normalized.canvasId = "";
        if (!normalized.idempotencyKey) normalized.idempotencyKey = `${this.identityKey}:${this.now()}`;
        return normalized;
    }

    private currentServerConversationId() {
        const record = this.document.conversations.find((item) => item.id === this.stateValue.activeConversationId);
        return record?.serverConversationId || record?.run?.conversationId || this.stateValue.run?.conversationId || this.stateValue.run?.id || "";
    }

    private async ensureActiveRecord(messages = this.stateValue.messages) {
        const activeId = this.stateValue.activeConversationId || this.createId();
        const existing = this.document.conversations.find((item) => item.id === activeId);
        const record: AgentConversationRecord = existing
            ? { ...existing, messages }
            : {
                  id: activeId,
                  title: this.titleFor(messages),
                  messages,
                  run: this.stateValue.run,
                  permissionMode: "read_only",
                  model: this.stateValue.model || undefined,
                  reasoningMode: this.stateValue.reasoningMode,
                  skillIds: this.stateValue.skillIds,
                  contextUsage: this.stateValue.contextUsage,
                  sendMode: this.stateValue.sendMode,
                  createdAt: this.now(),
                  updatedAt: this.now(),
              };
        this.document = {
            version: 2,
            activeId,
            conversations: existing ? this.document.conversations.map((item) => (item.id === activeId ? record : item)) : [record, ...this.document.conversations],
        };
        this.set({ activeConversationId: activeId, conversations: this.document.conversations });
        await this.store.saveDocument(this.identity, this.document);
        return record;
    }

    private async loadHistory(localId: string, conversationId: string, viewVersion: number) {
        if (!this.isCurrentView(localId, viewVersion)) return;
        const version = ++this.historyVersion;
        const initialRunId = this.stateValue.run?.id;
        const current = () => this.isCurrentView(localId, viewVersion) && version === this.historyVersion && initialRunId === this.stateValue.run?.id;
        try {
            const runs: AgentRunSnapshot[] = [];
            let after: string | undefined;
            do {
                const page = await this.client.listConversationRuns(conversationId, { after, limit: 100 }, this.signal());
                if (!current()) return;
                runs.push(...page.items);
                after = page.nextCursor;
            } while (after);
            const restored: AgentRunSnapshot[] = [];
            for (const run of runs) {
                if (run.conversationId !== conversationId) throw new Error("Agent 轮次归属不一致，已停止恢复");
                restored.push(await this.loadFullRun(run));
                if (!current()) return;
            }
            const view = projectAgentConversation(conversationId, restored);
            const contextUsage = restored.reduce((usage, run) => [...(run.events || [])].sort((left, right) => left.seq - right.seq).reduce((next, event) => reduceAgentContextUsage(next, event), usage), emptyAgentContextUsage(restored.at(-1)?.id || ""));
            const record = this.record(localId);
            if (!record || !current()) return;
            this.stopEvents();
            // A directory response that started before this handoff may still describe the previous run.
            if (this.stateValue.run?.id !== view.run?.id) ++this.runVersion;
            const merged = { ...this.mergeRecord(record, view, restored, conversationId), contextUsage };
            this.replaceRecord(merged);
            this.set({
                messages: merged.messages,
                run: merged.run,
                approval: merged.run?.approval || null,
                error: "",
                contextUsage,
                model: merged.model || merged.run?.model || this.stateValue.model,
                reasoningMode: merged.reasoningMode || this.stateValue.reasoningMode,
                skillIds: merged.skillIds || this.stateValue.skillIds,
                connection: activeRun(merged.run) ? "connecting" : "connected",
            });
            if (activeRun(merged.run)) this.connectRun(merged.run!);
            this.schedulePersist();
            await this.refreshQueue();
        } catch (cause) {
            if (current()) {
                this.set({ error: cause instanceof Error ? cause.message : "Agent 历史读取失败" });
                this.updateRecord({ historyState: { status: "unavailable", message: "当前对话暂时无法恢复，本地记录已保留" } });
                this.schedulePersist();
            }
        }
    }

    private async loadFullRun(source: AgentRunSnapshot) {
        const prefix = this.uniqueEvents(source);
        const events = prefix.every((event, index) => event.seq === index + 1) ? prefix : [];
        let seq = events.at(-1)?.seq || 0;
        let missingPrefix = 0;
        let total = source.eventCount || 0;
        let snapshot = source;
        do {
            const { run } = await this.client.getRun(source.id, this.signal(), seq === 0 ? { fromStart: true, eventLimit: 500 } : { sinceSeq: seq, eventLimit: 500 });
            this.signal().throwIfAborted();
            if (run.id !== source.id || run.conversationId !== source.conversationId) throw new Error("Agent 轮次归属不一致，已停止恢复");
            const page = this.uniqueEvents(run).filter((event) => event.seq > seq);
            total = Math.max(total, run.eventCount || 0);
            // Legacy checkpoints may explicitly declare a prefix that no longer exists.
            if (seq === 0 && run.eventSeqBase && page[0]?.seq === run.eventSeqBase + 1 && total >= page.at(-1)!.seq) {
                seq = missingPrefix = run.eventSeqBase;
            }
            if (page.some((event, index) => event.seq !== seq + index + 1) || (!page.length && seq < total)) throw new Error("Agent 历史事件未完整返回，请重试读取");
            snapshot = run;
            events.push(...page);
            seq = events.at(-1)?.seq || 0;
        } while (seq < total);
        return { ...snapshot, events, latestSeq: seq, eventSeqBase: missingPrefix, eventsTruncated: missingPrefix > 0 };
    }

    private uniqueEvents(run: AgentRunSnapshot) {
        const events = (run.events || []).filter((event) => event.runId === run.id && Number.isSafeInteger(event.seq) && event.seq > 0);
        return [...new Map(events.map((event) => [event.seq, event])).values()].sort((a, b) => a.seq - b.seq);
    }

    private connectRun(run: AgentRunSnapshot) {
        this.stopEvents();
        if (!run.id || this.disposed) return;
        const localId = this.stateValue.activeConversationId;
        const controller = new AbortController();
        this.eventController = controller;
        const current = () => this.isCurrentView(localId) && !controller.signal.aborted && this.stateValue.run?.id === run.id;
        let lastSeq = this.record(localId)?.eventCursors?.[run.id] || 0;
        this.set({ connection: "connecting" });
        void this.client
            .readEvents(
                run.id,
                (event) => {
                    if (!current()) return;
                    if (event.seq > 0) {
                        if (event.seq <= lastSeq) return;
                        lastSeq = event.seq;
                    }
                    const next = applyAgentConversationEvent({ messages: this.stateValue.messages, run: this.stateValue.run, approval: this.stateValue.approval }, event);
                    const contextUsage = reduceAgentContextUsage(this.stateValue.contextUsage, event);
                    this.set({ ...next, contextUsage, connection: "connected" });
                    this.updateRecord({
                        messages: next.messages,
                        run: next.run,
                        contextUsage,
                        lastEventSeq: lastSeq,
                        eventCursors: { ...this.record(localId)?.eventCursors, [run.id]: lastSeq },
                    });
                    for (const listener of this.eventListeners) listener(event);
                    this.schedulePersist();
                    if (["user_question", "run_completed", "run_failed", "run_cancelled"].includes(event.type)) void this.refreshQueue();
                },
                {
                    after: lastSeq,
                    signal: controller.signal,
                    onSnapshot: (snapshot) => {
                        if (!current()) return;
                        this.set({ run: snapshot, approval: snapshot.approval || null, connection: "connected", error: "" });
                        this.updateRecord({ run: snapshot });
                        this.schedulePersist();
                    },
                },
            )
            .catch((cause) => {
                if (current()) this.set({ connection: "disconnected", error: cause instanceof Error ? cause.message : "Agent 事件流已断开" });
            });
    }

    private stopEvents() {
        this.eventController?.abort();
        this.eventController = null;
    }

    private mergeRecord(local: AgentConversationRecord, view: AgentConversationView, runs: AgentRunSnapshot[], conversationId: string): AgentConversationRecord {
        const remoteIds = new Set(runs.map((run) => run.id));
        const remoteUserMessages = new Set(view.messages.filter((message) => message.role === "user").map((message) => message.text.trim()));
        const remoteMessageIds = new Set(view.messages.map((message) => message.id));
        const kept = local.messages.filter(
            (message) => !remoteMessageIds.has(message.id) && !(message.role === "user" && remoteUserMessages.has(message.text.trim())) && !message.runId?.startsWith("local-") && ![...remoteIds].some((id) => message.id.includes(id)),
        );
        const eventCursors = { ...(local.eventCursors || {}) };
        for (const run of runs) {
            eventCursors[run.id] = run.latestSeq || 0;
        }
        return {
            ...local,
            serverConversationId: conversationId,
            runs,
            messages: [...view.messages, ...kept],
            run: view.run,
            lastEventSeq: Math.max(local.lastEventSeq || 0, ...runs.map((run) => run.latestSeq || 0)),
            eventCursors,
            title: local.title || this.titleFor(view.messages),
            updatedAt: view.run?.updatedAt || local.updatedAt,
            permissionMode: view.run?.permissionMode || local.permissionMode,
            model: view.run?.model || local.model,
            reasoningMode: local.reasoningMode,
            skillIds: local.skillIds || view.run?.skills?.map((skill) => skill.id),
            contextUsage: local.contextUsage,
            sendMode: local.sendMode,
            historyState: runs.some((run) => run.eventsTruncated) ? { status: "partial", message: "旧运行的部分早期事件无法恢复" } : undefined,
            draft: local.draft,
        };
    }

    private replaceRecord(record: AgentConversationRecord) {
        const conversations = this.document.conversations.map((item) => (item.id === record.id ? record : item));
        this.document = { version: 2, activeId: this.stateValue.activeConversationId, conversations };
        this.set({ conversations });
    }

    private patchRecord(id: string, patch: Partial<AgentConversationRecord>) {
        const record = this.record(id);
        if (!record) return;
        this.replaceRecord({ ...record, ...patch });
    }

    private updateRecord(patch: Partial<AgentConversationRecord>) {
        this.patchRecord(this.stateValue.activeConversationId, patch);
    }

    private titleFor(messages: AgentConversationRecord["messages"]) {
        const text = messages.find((message) => message.role === "user")?.text.trim() || "新对话";
        return text.length > 28 ? `${text.slice(0, 28)}...` : text;
    }

    private schedulePersist() {
        if (this.persistTimer) clearTimeout(this.persistTimer);
        this.persistTimer = setTimeout(() => {
            if (this.disposed) return;
            void this.store.saveDocument(this.identity, this.document).catch(() => undefined);
        }, 180);
    }

    private set(patch: Partial<AgentConversationControllerState>) {
        this.stateValue = { ...this.stateValue, ...patch };
        for (const listener of this.listeners) listener();
    }
}
