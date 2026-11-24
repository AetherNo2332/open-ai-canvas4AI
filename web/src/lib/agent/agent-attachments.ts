import { agentNodeMentionToken, agentResourceMentionToken, type AgentReference } from "@/lib/agent/reference";

export const AGENT_ATTACHMENT_MAX = 9;
export const AGENT_LONG_TEXT_CHARS = 2000;
const AGENT_IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/webp", "image/gif"]);

export type AgentAttachmentStatus = "pending" | "uploading" | "ready" | "error" | "local_only";
export type AgentAttachmentState = { status: AgentAttachmentStatus; progress?: number; error?: string };

export function agentAttachmentFileKey(file: Pick<File, "name" | "size" | "lastModified">) {
    return `${file.name}:${file.size}:${file.lastModified}`;
}

export function appendAgentNodeReferences(text: string, nodeIds: readonly string[]) {
    const missing = [...new Set(nodeIds)].map(agentNodeMentionToken).filter((token) => !text.includes(token));
    if (!missing.length) return text;
    const body = text.replace(/\s+$/u, "");
    return body ? `${body}\n${missing.join(" ")}` : missing.join(" ");
}

export function removeAgentNodeReference(text: string, nodeId: string) {
    const token = agentNodeMentionToken(nodeId);
    if (!text.includes(token)) return text;
    return text
        .split(token)
        .join("")
        .replace(/[ \t]{2,}/gu, " ")
        .replace(/[ \t]+\n/gu, "\n")
        .trim();
}

export type AgentAttachmentFileCheck = { accepted: File[]; problems: string[]; hasGif: boolean };

export function checkAgentAttachmentFiles(files: readonly File[], options: { existingCount: number; existingKeys: ReadonlySet<string>; maxBytes: number }): AgentAttachmentFileCheck {
    const seen = new Set<string>();
    const accepted: File[] = [];
    const problems: string[] = [];
    let hasGif = false;
    for (const file of files) {
        const key = agentAttachmentFileKey(file);
        if (seen.has(key)) continue;
        seen.add(key);
        if (options.existingKeys.has(key)) {
            problems.push("这张图片已经添加过");
            continue;
        }
        if (/^image\/hei[cf]/u.test(file.type) || /\.(heic|heif)$/iu.test(file.name)) {
            problems.push("暂不支持 HEIC 图片，请导出为 JPG 或 PNG 后再添加");
            continue;
        }
        if (!AGENT_IMAGE_TYPES.has(file.type)) {
            problems.push(`「${file.name || "文件"}」不是 PNG、JPG、WebP 或 GIF 图片`);
            continue;
        }
        if (file.size > options.maxBytes) {
            problems.push(`「${file.name || "图片"}」超过 ${Math.round(options.maxBytes / 1024 / 1024)}MB 上传上限`);
            continue;
        }
        if (options.existingCount + accepted.length >= AGENT_ATTACHMENT_MAX) {
            problems.push(`每条消息最多 ${AGENT_ATTACHMENT_MAX} 张图片`);
            break;
        }
        if (file.type === "image/gif") hasGif = true;
        accepted.push(file);
    }
    return { accepted, problems: [...new Set(problems)], hasGif };
}

export function agentCopyText(text: string, references: readonly AgentReference[]) {
    let value = text;
    for (const reference of references) {
        if (reference.nodeId) value = value.split(agentNodeMentionToken(reference.nodeId)).join(`@${reference.label || reference.title || "引用"}`);
        const token = agentResourceMentionToken(reference);
        if (token.startsWith("@[") && value.includes(token)) value = value.split(token).join(`@${reference.label || reference.title || "引用"}`);
    }
    return value.replace(/@\[[^\]]+\]/gu, "@引用");
}
