import type { Skill } from "@/services/api/skills";

/**
 * A reference understood by the shared Agent composer and message renderer.
 * Canvas resources, uploaded files and skills all lower to this shape; the
 * owning business adapter decides how a reference is resolved or focused.
 */
export type AgentReference = {
    id: string;
    nodeId: string;
    kind?: string;
    label: string;
    title?: string;
    previewUrl?: string;
    mediaUrl?: string;
    text?: string;
    active?: boolean;
    sourceType?: string;
    skill?: Skill;
    assetId?: string;
    mentionToken?: string;
};

export function agentNodeMentionToken(nodeId: string) {
    return `@[node:${nodeId}]`;
}

export function agentSkillMentionToken(skillId: string) {
    return `@[skill:${skillId}]`;
}

export function agentResourceMentionToken(reference: AgentReference) {
    if (reference.mentionToken) return reference.mentionToken;
    if (reference.kind === "skill" && reference.skill?.skillId) return agentSkillMentionToken(reference.skill.skillId);
    if (reference.assetId) return `@[asset:${reference.assetId}]`;
    if (reference.nodeId) return agentNodeMentionToken(reference.nodeId);
    return `@${reference.label}`;
}
