import { useRef } from "react";
import { App } from "antd";
import { useCanvasEditLease } from "./use-canvas-edit-lease";
import type { CanvasNodeData } from "@/types/canvas";
import { captureCanvasAuthorIdentity, sameCanvasAuthorIdentity, clearCanvasAuthorDraft, readCanvasAuthorDraft, saveCanvasAuthorDraft, type CanvasAuthorIdentity } from "@/services/canvas-author-drafts";
import { getActiveUserScope } from "@/lib/user-scope";
import { flushCanvasCommandWorkspace } from "@/stores/canvas/use-canvas-session-store";

const fields = (node: CanvasNodeData) => ({ title: node.title, content: node.metadata?.content || "", richText: node.metadata?.richText || null });
export function useCanvasInlineAuthorEdit(canvasId: string, node: CanvasNodeData, active: boolean) {
    const { message, modal } = App.useApp();
    const lease = useCanvasEditLease(canvasId, node.id, active);
    const base = useRef<Record<string, unknown>>(fields(node));
    const identity = useRef<CanvasAuthorIdentity | undefined>(undefined);
    const current = useRef(node);
    current.current = node;
    const scope = lease.scope || getActiveUserScope();
    const retain = (value: Record<string, unknown>) => saveCanvasAuthorDraft(scope, canvasId, node.id, { kind: "text", identity: identity.current, base: base.current, value });
    return {
        ...lease,
        async enter(apply: (value: Record<string, unknown>) => void) {
            try {
                base.current = fields(current.current);
                identity.current = captureCanvasAuthorIdentity(canvasId, node.id);
                await lease.begin();
                if (scope !== getActiveUserScope() || (lease.controller && !lease.controller.isCurrent())) return;
                const draft = await readCanvasAuthorDraft(scope, canvasId, node.id, "text");
                if (scope !== getActiveUserScope() || (lease.controller && !lease.controller.isCurrent())) return;
                if (
                    draft &&
                    ((lease.isRemote && !sameCanvasAuthorIdentity(draft.identity, captureCanvasAuthorIdentity(canvasId, node.id))) ||
                        (JSON.stringify(draft.base) !== JSON.stringify(fields(current.current)) && JSON.stringify(draft.value) !== JSON.stringify(fields(current.current))))
                ) {
                    modal.confirm({
                        title: "旧草稿与云端版本不同",
                        content: `请先复制保留旧草稿，再采用云端继续编辑。旧标题：${String(draft.value.title || "")}；旧正文：${String(draft.value.content || "")}`,
                        okText: "采用云端继续",
                        cancelText: "保留草稿",
                        onOk: async () => {
                            await lease.begin();
                            if (scope !== getActiveUserScope() || (lease.controller && !lease.controller.isCurrent())) return;
                            await clearCanvasAuthorDraft(scope, canvasId, node.id, "text");
                            base.current = fields(current.current);
                            identity.current = captureCanvasAuthorIdentity(canvasId, node.id);
                            apply(fields(current.current));
                        },
                    });
                    return;
                }
                if (draft) base.current = draft.base;
                if (draft) identity.current = draft.identity;
                apply(draft?.value || fields(current.current));
            } catch (error) {
                message.error(error instanceof Error ? error.message : "暂时无法获取编辑占用");
            }
        },
        remember(patch: Record<string, unknown>) {
            void retain({ ...fields(current.current), ...patch }).catch(() => message.error("本地草稿保存失败，请复制并保留当前内容"));
        },
        async commit(patch: Record<string, unknown>, apply: () => void) {
            try {
                await retain({ ...fields(current.current), ...patch });
                if (lease.isRemote && !sameCanvasAuthorIdentity(identity.current, captureCanvasAuthorIdentity(canvasId, node.id))) throw new Error("节点版本已改变，旧草稿已保留");
                const latest = fields(current.current) as Record<string, unknown>;
                for (const [key, value] of Object.entries(patch))
                    if (JSON.stringify(latest[key]) !== JSON.stringify(base.current[key]) && JSON.stringify(latest[key]) !== JSON.stringify(value)) throw new Error("云端作者内容已改变，旧草稿已保留，请核对后重试");
                await lease.save(async () => {
                    apply();
                    if (lease.isRemote && !(await flushCanvasCommandWorkspace())) throw new Error("云端保存尚未确认，草稿已保留，请重试");
                });
                await clearCanvasAuthorDraft(scope, canvasId, node.id, "text");
                return true;
            } catch (error) {
                message.error(error instanceof Error ? error.message : "保存尚未确认，草稿已保留");
                return false;
            }
        },
    };
}
