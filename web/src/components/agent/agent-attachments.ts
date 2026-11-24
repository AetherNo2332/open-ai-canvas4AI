import { translate } from "@/i18n";
import { agentNodeMentionToken, type AgentReference } from "@/lib/agent/reference";
import type { AgentAttachmentStatus } from "@/lib/agent/agent-attachments";
import type { AgentChatAttachment } from "@/lib/agent/chat-types";

export type CloudAgentChatAttachment = AgentChatAttachment;

/** 附件引用候选，编号按附件顺序从「图片1」开始；绑定到业务对象后使用对应引用。 */
export function agentAttachmentReferences(attachments: CloudAgentChatAttachment[]): AgentReference[] {
    return attachments
        .filter((item) => !item.status || item.status === "ready")
        .map((item, index) =>
            item.nodeId
                ? {
                      id: item.nodeId,
                      nodeId: item.nodeId,
                      kind: item.kind === "text" ? "text" : item.kind === "video" ? "video" : item.kind === "audio" ? "audio" : "image",
                      label: item.name || translate("canvasA.attachments.imageLabel", { index: index + 1 }),
                      title: item.name || translate("canvasA.attachments.imageLabel", { index: index + 1 }),
                      previewUrl: item.url || undefined,
                      active: true,
                      mentionToken: agentNodeMentionToken(item.nodeId),
                  }
                : {
                      id: `attachment:${item.id}`,
                      nodeId: "",
                      kind: "image",
                      label: translate("canvasA.attachments.imageLabel", { index: index + 1 }),
                      title: item.name || translate("canvasA.attachments.imageLabel", { index: index + 1 }),
                      previewUrl: item.url,
                      active: true,
                      mentionToken: `@[attachment:${item.id}]`,
                  },
        );
}
