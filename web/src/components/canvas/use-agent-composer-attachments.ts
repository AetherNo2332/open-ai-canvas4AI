import { useLayoutEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { nanoid } from "nanoid";
import { agentAttachmentFileKey, appendAgentNodeReferences, checkAgentAttachmentFiles, removeAgentNodeReference } from "@/lib/canvas/agent-composer-attachments";
import { canvasNodeMentionToken, type CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { getActiveUserScope } from "@/lib/user-scope";
import type { AgentCanvasContext } from "@/stores/use-global-agent-store";
import type { CloudAgentChatAttachment } from "./canvas-cloud-agent-attachments";

type UploadAttachment = CloudAgentChatAttachment & { file: File; fileKey: string; uploadNodeId: string };
type Options = {
    scope: string;
    userScope: string;
    prompt: string;
    references: CanvasResourceReference[];
    insertFiles: AgentCanvasContext["insertAgentFiles"];
    maxBytes: number;
    onPromptChange: Dispatch<SetStateAction<string>>;
    onWarning: (message: string) => void;
};

/** 原文件只活在当前输入作用域，上传成功后由画布节点承担引用和持久化。 */
export function useAgentComposerAttachments({ scope, userScope, prompt, references, insertFiles, maxBytes, onPromptChange, onWarning }: Options) {
    const uploads = useMemo(() => ({ controller: new AbortController(), items: new Map<string, UploadAttachment>() }), [scope]);
    const current = useRef(uploads);
    current.current = uploads;
    const [, render] = useState(0);
    useLayoutEffect(() => {
        // StrictMode 的 effect 重放也要恢复本次作用域的可用取消信号。
        if (uploads.controller.signal.aborted) uploads.controller = new AbortController();
        return () => {
            uploads.controller.abort();
            for (const item of uploads.items.values()) URL.revokeObjectURL(item.url);
            uploads.items.clear();
        };
    }, [uploads]);
    const isCurrent = () => current.current === uploads && !uploads.controller.signal.aborted && getActiveUserScope() === userScope;
    const referenced = references
        .filter((reference) => reference.kind === "image" && reference.nodeId && prompt.includes(canvasNodeMentionToken(reference.nodeId)))
        .map((reference) => ({ id: reference.nodeId, nodeId: reference.nodeId, name: reference.label, url: reference.previewUrl || "" }));

    const upload = async (item: UploadAttachment) => {
        if (!insertFiles || !isCurrent() || uploads.items.get(item.id) !== item || item.status === "uploading") return;
        item.status = "uploading";
        item.error = undefined;
        render((revision) => revision + 1);
        try {
            const ids = await insertFiles([item.file], uploads.controller.signal, [item.uploadNodeId]);
            if (!isCurrent() || uploads.items.get(item.id) !== item) return;
            if (ids.length !== 1 || !ids[0]) throw new Error("图片尚未保存到服务器，请重试上传");
            onPromptChange((value) => appendAgentNodeReferences(value, ids));
            uploads.items.delete(item.id);
            URL.revokeObjectURL(item.url);
        } catch (error) {
            if (!isCurrent() || uploads.items.get(item.id) !== item) return;
            item.status = "error";
            item.error = error instanceof Error ? error.message : "图片上传失败，请重试";
        }
        if (isCurrent()) render((revision) => revision + 1);
    };

    const addFiles = async (input: FileList | File[] | null) => {
        if (!insertFiles || !isCurrent()) return;
        const check = checkAgentAttachmentFiles(Array.from(input || []), {
            existingCount: referenced.length + uploads.items.size,
            existingKeys: new Set([...uploads.items.values()].map((item) => item.fileKey)),
            maxBytes,
        });
        if (check.problems.length) onWarning(check.problems.join("；"));
        const added = check.accepted.map((file): UploadAttachment => {
            const id = nanoid();
            return { id, uploadNodeId: `image-${id}`, name: file.name, url: URL.createObjectURL(file), file, fileKey: agentAttachmentFileKey(file), status: "pending", retryable: true };
        });
        for (const item of added) uploads.items.set(item.id, item);
        render((revision) => revision + 1);
        for (const item of added) await upload(item);
    };
    const remove = (id: string) => {
        if (!isCurrent()) return;
        const item = uploads.items.get(id);
        if (item) {
            uploads.items.delete(id);
            URL.revokeObjectURL(item.url);
            render((revision) => revision + 1);
        } else onPromptChange((value) => removeAgentNodeReference(value, id));
    };
    const retry = (id: string) => {
        const item = uploads.items.get(id);
        if (item?.status === "error") void upload(item);
    };
    const pending = [...uploads.items.values()];
    const blockReason = pending.some((item) => item.status !== "error") ? "图片正在上传到画布，请稍候" : pending.length ? "图片上传失败，请重试或移除失败附件" : undefined;
    return { attachments: [...referenced, ...pending], addFiles, remove, retry, blockReason };
}
