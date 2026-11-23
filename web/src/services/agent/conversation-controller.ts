import type {
    AgentApprovalDecision,
    AgentApprovalSnapshot,
    AgentBusinessScope,
    AgentConversation,
    AgentConversationQueue,
    AgentEvent,
    AgentMessageInput,
    AgentQueuedMessage,
    AgentRunRequest,
    AgentRunSnapshot,
} from "@/sdk/agent";
import type { createAgentClient } from "@/sdk/agent/client";
import { defaultAgentConversationStore, type AgentConversationStore } from "./conversation-storage";
import type {
    AgentConversationDocument,
    AgentConversationIdentity,
    AgentConversationRecord,
    AgentConversationView,
    AgentPendingSubmission,
} from "./conversation-types";
import { applyAgentConversationEvent, projectAgentConversation } from "@/lib/agent/conversation-events";

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
};

const terminal = new Set<AgentRunSnapshot["status"]>(["completed", "failed", "cancelled", "rejected"]);

function activeRun(run: AgentRunSnapshot | null | undefined) {
    return Boolean(run && (run.cleanupPending || ["queued", "running", "waiting_approval"].includes(run.status)));
}

function sameScope(left: AgentBusinessScope, right: AgentBusinessScope) {
    return left.type === right.type && left.id === right.id;
}

function recordFromConversation(summary: AgentConversation, existing?: AgentConversationRecord): AgentConversationRecord {
    return {
        id: existing?.id || summary.id,
        serverConversationId: summary.id,
        title: summary.title,
        messages: existing?.messages || [],
        run: existing?.run || null,
        lastEventSeq: existing?.lastEventSeq,
        historyState: existing?.historyState,
        draft: existing?.draft,
        permissionMode: existing?.permissionMode || "read_only",
        model: existing?.model,
        skillIds: existing?.skillIds,
        archived: summary.archived,
        createdAt: existing?.createdAt || summary.createdAt,
        updatedAt: summary.updatedAt,
        adapterData: existing?.adapterData,
    };
}

function sameRecord(left: AgentConversationRecord, right: AgentConversationRecord) {
    return JSON.stringify(left) === JSON.stringify(right);
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
    private stateValue: AgentConversationControllerState;
    private document: AgentConversationDocument = { version: 2, activeId: null, conversations: [] };
    private eventController: AbortController | null = null;
    private disposed = false;
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
        };
    }

    subscribe = (listener: () => void) => {
        this.listeners.add(listener);
        return () => this.listeners.delete(listener);
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
            const draft = active ? (await this.store.loadDraft(this.identity, active.id)) || active.draft || "" : "";
            const pending = active ? await this.store.loadPending(this.identity, active.id) : null;
            if (this.disposed) return;
            this.set({
                status: "ready",
                activeConversationId,
                messages: active?.messages || [],
                run: active?.run || null,
                approval: active?.run?.approval || null,
                conversations: document.conversations,
                draft,
                pending,
            });
            if (!active) await this.ensureActiveRecord();
            if (active?.serverConversationId || active?.run?.conversationId) await this.loadHistory(active.serverConversationId || active.run!.conversationId!);
            else await this.refreshDirectory();
        } catch (cause) {
            if (!this.disposed) this.set({ status: "error", error: cause instanceof Error ? cause.message : "Agent 会话恢复失败" });
        }
    }

    async refreshDirectory() {
        if (this.disposed) return;
        try {
            const items: AgentConversation[] = [];
            let before: string | undefined;
            do {
                const page = await this.client.listConversations({ before, limit: 100 }, this.signal());
                if (this.disposed) return;
                items.push(...page.items);
                before = page.nextCursor;
            } while (before);
            const existing = new Map(this.document.conversations.map((record) => [record.serverConversationId || record.id, record]));
            const conversations = this.document.conversations.map((record) => {
                const summary = items.find((item) => item.id === (record.serverConversationId || record.id));
                return summary ? recordFromConversation(summary, record) : record;
            });
            for (const summary of items) {
                if (!conversations.some((record) => record.serverConversationId === summary.id || record.id === summary.id)) conversations.push(recordFromConversation(summary, existing.get(summary.id)));
            }
            this.document = { version: 2, activeId: this.stateValue.activeConversationId || null, conversations };
            this.set({ conversations });
            const active = conversations.find((record) => record.id === this.stateValue.activeConversationId);
            if (active?.serverConversationId && (!active.run || active.run.id !== this.summaryFor(active)?.latestRunId)) await this.loadHistory(active.serverConversationId);
            this.schedulePersist();
        } catch (cause) {
            if (!this.disposed) this.set({ error: cause instanceof Error ? cause.message : "Agent 目录读取失败" });
        }
    }

    async openConversation(id: string) {
        const record = this.document.conversations.find((item) => item.id === id);
        if (!record) return;
        this.stopEvents();
        const draft = await this.store.loadDraft(this.identity, id);
        const pending = await this.store.loadPending(this.identity, id).catch((cause) => {
            this.set({ error: cause instanceof Error ? cause.message : "待确认请求读取失败" });
            return null;
        });
        if (this.disposed) return;
        this.document = { ...this.document, activeId: id };
        this.set({
            activeConversationId: id,
            messages: record.messages,
            run: record.run,
            approval: record.run?.approval || null,
            draft: draft || record.draft || "",
            pending,
            error: "",
        });
        if (record.serverConversationId || record.run?.conversationId) await this.loadHistory(record.serverConversationId || record.run!.conversationId!);
        this.schedulePersist();
    }

    async newConversation() {
        this.stopEvents();
        const id = this.createId();
        const record: AgentConversationRecord = {
            id,
            title: "新对话",
            messages: [],
            run: null,
            permissionMode: "read_only",
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
        });
        await this.store.clearDraft(this.identity, id);
        await this.store.saveDocument(this.identity, this.document);
    }

    setDraft(draft: string) {
        this.set({ draft });
        void this.store.saveDraft(this.identity, this.stateValue.activeConversationId, draft);
        this.schedulePersist();
    }

    async submit(options: AgentConversationSubmitOptions) {
        if (this.disposed || this.stateValue.busy) return false;
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
                      mode: options.mode || (this.stateValue.queue?.awaitingQuestionId ? "interject" : "queue"),
                      expectedRunId: this.stateValue.run.id,
                      ...(options.replyToQuestionId ? { replyToQuestionId: options.replyToQuestionId } : {}),
                  }
                : undefined;
        const pending: AgentPendingSubmission =
            previous?.request
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
        this.set({ busy: true, error: "", ...(previous?.request ? {} : { pending }) });
        try {
            await this.prepare?.();
            if (this.disposed) return false;
            const messages = options.preserveDraft
                ? this.stateValue.messages
                : this.stateValue.messages.some((message) => message.id === pending.messageId)
                  ? this.stateValue.messages
                  : [...this.stateValue.messages, { id: pending.messageId || `user-${pending.key}`, role: "user" as const, text: request.prompt }];
            await this.ensureActiveRecord(messages);
            await this.store.savePending(this.identity, this.stateValue.activeConversationId, pending);
            this.set({
                pending,
                messages,
            });
            await this.store.saveDocument(this.identity, this.document);
            let nextRun: AgentRunSnapshot | undefined;
            let conversationDelta: AgentConversationQueue | undefined;
            let receiptConversationId = "";
            if (pending.conversationMessage) {
                const { conversationId: target, ...message } = pending.conversationMessage;
                const receipt = await this.client.sendMessage(target, { ...message, request: pending.request! }, this.signal());
                receiptConversationId = receipt.conversationId || target;
                conversationDelta = await this.client.getConversationQueue(target, this.signal());
                if (receipt.runId) nextRun = (await this.client.getRun(receipt.runId, this.signal(), { eventLimit: 1 })).run;
            } else if (pending.parentRunId) {
                nextRun = (await this.client.continueRun(pending.parentRunId, pending.request!, this.signal())).run;
            } else {
                nextRun = (await this.client.startRun(pending.request!, this.signal())).run;
            }
            if (this.disposed) return false;
            await this.store.clearPending(this.identity, this.stateValue.activeConversationId);
            if (nextRun) {
                const messages = this.stateValue.messages.map((message) =>
                    message.id === pending.messageId ? { ...message, id: `user-${nextRun!.id}`, runId: nextRun!.id } : message,
                );
                const conversationId = nextRun.conversationId || receiptConversationId;
                this.set({ messages, run: nextRun, approval: nextRun.approval || null, pending: null, connection: "connecting", ...(conversationDelta ? { queue: conversationDelta } : {}) });
                this.updateRecord({
                    messages,
                    run: nextRun,
                    pending: null,
                    ...(conversationId ? { serverConversationId: conversationId } : {}),
                    ...(conversationDelta ? { queue: conversationDelta } : {}),
                });
                this.connectRun(nextRun);
            } else {
                await this.refreshQueue();
                if (receiptConversationId) this.updateRecord({ serverConversationId: receiptConversationId });
                this.set({ pending: null });
                this.updateRecord({ pending: null });
            }
            if (!options.preserveDraft) this.setDraft("");
            this.schedulePersist();
            return true;
        } catch (cause) {
            if (this.disposed) return false;
            const status = (cause as { status?: number }).status;
            const reason = (cause as { reason?: string }).reason || "";
            const definitive = Boolean(status && [400, 401, 403, 404, 422].includes(status)) || ["agent_conversation_stale_parent", "agent_question_pending", "agent_question_answer_pending", "agent_question_stale"].includes(reason);
            if (definitive) {
                await this.store.clearPending(this.identity, this.stateValue.activeConversationId).catch(() => undefined);
                this.set({ pending: null });
                this.updateRecord({ pending: null });
            }
            this.set({ error: cause instanceof Error ? cause.message : "Agent 请求失败，可重试原消息" });
            return false;
        } finally {
            if (!this.disposed) this.set({ busy: false });
        }
    }

    async retryPending() {
        const pending = this.stateValue.pending;
        if (!pending?.request) return false;
        return this.submit({ request: pending.request, preserveDraft: true });
    }

    async refreshQueue() {
        const conversationId = this.currentServerConversationId();
        if (!conversationId) return;
        try {
            const queue = await this.client.getConversationQueue(conversationId, this.signal());
            if (!this.disposed) this.set({ queue });
        } catch (cause) {
            if (!this.disposed) this.set({ error: cause instanceof Error ? cause.message : "待发消息读取失败" });
        }
    }

    async editQueued(item: AgentQueuedMessage, prompt: string) {
        const conversationId = this.currentServerConversationId();
        if (!conversationId) return false;
        await this.client.updateQueuedMessage(conversationId, item.id, { revision: item.revision, prompt }, this.signal());
        await this.refreshQueue();
        return true;
    }

    async interjectQueued(item: AgentQueuedMessage) {
        const conversationId = this.currentServerConversationId();
        if (!conversationId) return false;
        await this.client.updateQueuedMessage(conversationId, item.id, { revision: item.revision, action: "interject" }, this.signal());
        await this.refreshQueue();
        return true;
    }

    async cancelQueued(item: AgentQueuedMessage) {
        const conversationId = this.currentServerConversationId();
        if (!conversationId) return false;
        await this.client.cancelQueuedMessage(conversationId, item.id, item.revision, this.signal());
        await this.refreshQueue();
        return true;
    }

    async updateQueue(action: "pause" | "resume") {
        const conversationId = this.currentServerConversationId();
        if (!conversationId || !this.stateValue.queue) return false;
        const queue = await this.client.updateConversationQueue(conversationId, { revision: this.stateValue.queue.revision, action }, this.signal());
        this.set({ queue });
        return true;
    }

    async cancelRun() {
        const run = this.stateValue.run;
        if (!run) return false;
        const receipt = await this.client.cancelRun(run.id, this.signal());
        if (receipt.accepted) {
            const { run: next } = await this.client.getRun(run.id, this.signal(), { eventLimit: 1 });
            this.set({ run: next, approval: next.approval || null });
        }
        return receipt.accepted;
    }

    async decideApproval(decision: "approve" | "reject", input: Omit<AgentApprovalDecision, "decision"> = {}) {
        const run = this.stateValue.run;
        const approval = this.stateValue.approval;
        if (!run || !approval) return false;
        await this.client.decideApproval(run.id, approval.approvalId, { decision, ...input }, this.signal());
        this.set({ approval: null });
        return true;
    }

    async archiveConversation(id: string) {
        const record = this.document.conversations.find((item) => item.id === id);
        if (!record) return false;
        if (record.serverConversationId) await this.client.archiveConversation(record.serverConversationId, this.signal());
        const conversations = this.document.conversations.filter((item) => item.id !== id);
        this.document = { ...this.document, conversations };
        this.set({ conversations });
        if (id === this.stateValue.activeConversationId) await this.newConversation();
        this.schedulePersist();
        return true;
    }

    dispose() {
        this.disposed = true;
        this.stopEvents();
        if (this.persistTimer) clearTimeout(this.persistTimer);
        this.listeners.clear();
    }

    private signal() {
        return this.eventController?.signal;
    }

    private normalizeRequest(request: AgentRunRequest): AgentRunRequest {
        const scope = this.identity.scope;
        if (request.scope && !sameScope(request.scope, scope)) throw new Error("请求范围与当前页面不一致");
        const normalized = { ...request, scope } as AgentRunRequest;
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
                  createdAt: this.now(),
                  updatedAt: this.now(),
              };
        this.document = {
            version: 2,
            activeId,
            conversations: existing
                ? this.document.conversations.map((item) => (item.id === activeId ? record : item))
                : [record, ...this.document.conversations],
        };
        this.set({ activeConversationId: activeId, conversations: this.document.conversations });
        await this.store.saveDocument(this.identity, this.document);
        return record;
    }

    private summaryFor(record: AgentConversationRecord) {
        if (!record.serverConversationId) return undefined;
        return this.document.conversations.some((item) => item.serverConversationId === record.serverConversationId) ? { latestRunId: record.run?.id } : undefined;
    }

    private async loadHistory(conversationId: string) {
        try {
            const runs: AgentRunSnapshot[] = [];
            let after: string | undefined;
            do {
                const page = await this.client.listConversationRuns(conversationId, { after, limit: 100 }, this.signal());
                if (this.disposed) return;
                runs.push(...page.items);
                after = page.nextCursor;
            } while (after);
            const restored: AgentRunSnapshot[] = [];
            for (const run of runs) restored.push(await this.loadFullRun(run));
            const view = projectAgentConversation(conversationId, restored);
            const record = this.document.conversations.find((item) => item.id === this.stateValue.activeConversationId);
            if (!record) return;
            const merged = this.mergeRecord(record, view, restored, conversationId);
            this.replaceRecord(merged);
            this.set({ messages: merged.messages, run: merged.run, approval: merged.run?.approval || null, connection: activeRun(merged.run) ? "connecting" : "connected" });
            await this.refreshQueue();
            if (activeRun(merged.run)) this.connectRun(merged.run!);
        } catch (cause) {
            if (!this.disposed) {
                this.set({ error: cause instanceof Error ? cause.message : "Agent 历史读取失败" });
                this.updateRecord({ historyState: { status: "unavailable", message: "当前对话暂时无法恢复，本地记录已保留" } });
            }
        }
    }

    private async loadFullRun(source: AgentRunSnapshot) {
        const events = [...(source.events || [])].filter((event) => event.runId === source.id && event.seq > 0);
        const seen = new Set(events.map((event) => event.seq));
        let seq = Math.max(0, ...seen);
        let snapshot = source;
        let requestFromStart = seq === 0;
        for (let attempt = 0; attempt < 100; attempt += 1) {
            if (attempt > 0 && events.length >= (snapshot.eventCount || 0)) break;
            const { run } = await this.client.getRun(source.id, this.signal(), requestFromStart ? { fromStart: true, eventLimit: 500 } : { sinceSeq: seq, eventLimit: 500 });
            requestFromStart = false;
            const page = (run.events || []).filter((event) => event.runId === run.id && event.seq > seq && !seen.has(event.seq)).sort((a, b) => a.seq - b.seq);
            snapshot = { ...run, events: [...events, ...page] };
            if (!page.length) break;
            for (const event of page) {
                seen.add(event.seq);
                events.push(event);
            }
            seq = Math.max(seq, ...page.map((event) => event.seq));
        }
        return { ...snapshot, events };
    }

    private connectRun(run: AgentRunSnapshot) {
        this.stopEvents();
        if (!run.id) return;
        const controller = new AbortController();
        this.eventController = controller;
        const record = this.stateValue.conversations.find((item) => item.id === this.stateValue.activeConversationId);
        let lastSeq = Math.max(record?.eventCursors?.[run.id] || 0, ...(run.events || []).filter((event) => event.runId === run.id).map((event) => event.seq));
        this.set({ connection: "connecting" });
        void this.client
            .readEvents(
                run.id,
                (event) => {
                    if (this.disposed || controller.signal.aborted) return;
                    if (event.seq > 0) {
                        if (event.seq <= lastSeq) return;
                        lastSeq = event.seq;
                    }
                    const next = applyAgentConversationEvent({ messages: this.stateValue.messages, run: this.stateValue.run, approval: this.stateValue.approval }, event);
                    this.set({ ...next, connection: "connected" });
                    this.updateRecord({
                        messages: next.messages,
                        run: next.run,
                        lastEventSeq: lastSeq,
                        eventCursors: { ...(record?.eventCursors || {}), [run.id]: lastSeq },
                    });
                    this.schedulePersist();
                    if (["user_question", "run_completed", "run_failed", "run_cancelled"].includes(event.type)) void this.refreshQueue();
                },
                {
                    after: lastSeq,
                    signal: controller.signal,
                    onSnapshot: (snapshot) => {
                        if (this.disposed || controller.signal.aborted) return;
                        this.set({ run: snapshot, approval: snapshot.approval || null, connection: terminal.has(snapshot.status) ? "connected" : this.stateValue.connection });
                        this.updateRecord({ run: snapshot });
                    },
                },
            )
            .catch((cause) => {
                if (!this.disposed && !controller.signal.aborted) this.set({ connection: "disconnected", error: cause instanceof Error ? cause.message : "Agent 事件流已断开" });
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
            (message) =>
                !remoteMessageIds.has(message.id) &&
                !(message.role === "user" && remoteUserMessages.has(message.text.trim())) &&
                !message.runId?.startsWith("local-") &&
                ![...remoteIds].some((id) => message.id.includes(id)),
        );
        const eventCursors = { ...(local.eventCursors || {}) };
        for (const run of runs) {
            eventCursors[run.id] = Math.max(eventCursors[run.id] || 0, run.latestSeq || 0, ...(run.events || []).filter((event) => event.runId === run.id).map((event) => event.seq));
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
            skillIds: view.run?.skills?.map((skill) => skill.id) || local.skillIds,
            historyState: local.historyState,
            draft: local.draft,
        };
    }

    private replaceRecord(record: AgentConversationRecord) {
        const conversations = this.document.conversations.map((item) => (item.id === record.id ? record : item));
        this.document = { version: 2, activeId: this.stateValue.activeConversationId, conversations };
        this.set({ conversations });
    }

    private updateRecord(patch: Partial<AgentConversationRecord> & { pending?: null }) {
        const record = this.document.conversations.find((item) => item.id === this.stateValue.activeConversationId);
        if (!record) return;
        const next = { ...record, ...patch };
        delete (next as AgentConversationRecord & { pending?: null }).pending;
        this.replaceRecord(next);
    }

    private titleFor(messages: AgentConversationRecord["messages"]) {
        const text = messages.find((message) => message.role === "user")?.text.trim() || "新对话";
        return text.length > 28 ? `${text.slice(0, 28)}...` : text;
    }

    private schedulePersist() {
        if (this.persistTimer) clearTimeout(this.persistTimer);
        this.persistTimer = setTimeout(() => {
            if (this.disposed) return;
            const record = this.document.conversations.find((item) => item.id === this.stateValue.activeConversationId);
            if (record) {
                record.draft = this.stateValue.draft;
                record.messages = this.stateValue.messages;
                record.run = this.stateValue.run;
                record.updatedAt = this.now();
            }
            this.document = { version: 2, activeId: this.stateValue.activeConversationId, conversations: this.document.conversations };
            void this.store.saveDocument(this.identity, this.document).catch(() => undefined);
        }, 180);
    }

    private set(patch: Partial<AgentConversationControllerState>) {
        this.stateValue = { ...this.stateValue, ...patch };
        for (const listener of this.listeners) listener();
    }
}
