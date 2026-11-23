import type { ComponentType, ReactNode } from "react";
import { useSyncExternalStore } from "react";
import type { AgentCapabilities, AgentEvent, AgentRunRequest } from "@/sdk/agent";
import type { AgentConversationRecord } from "./conversation-types";
import type { AgentPageContext } from "./business-context";
import type { AgentReference } from "@/lib/agent/reference";
import type { AgentPromptEditorProps } from "@/components/agent/agent-prompt-editor";
import type { AgentChatAttachment } from "@/lib/agent/chat-types";

export type AgentPanelSelection = {
    model: string;
    logicalModelId?: string;
    channelId?: string;
    channelModelKey?: string;
    reasoningMode: NonNullable<AgentRunRequest["reasoningMode"]>;
    skillIds: string[];
    permissionMode: NonNullable<AgentRunRequest["permissionMode"]>;
    episodeId?: string;
    maxCredits?: number;
    focusNodeIds?: string[];
};

export type AgentPanelBuildRequestInput = {
    context: AgentPageContext;
    conversation: AgentConversationRecord | null;
    capabilities: AgentCapabilities;
    prompt: string;
    idempotencyKey: string;
    selection: AgentPanelSelection;
};

export type AgentPanelReference = AgentReference;

export type AgentPanelPrefill = {
    id: string | number;
    text: string;
    autoSubmit?: boolean;
    focusNodeIds?: string[];
    settle: (outcome: "sent" | "queued" | "prefilled" | false) => void;
};

export type AgentPanelBindings = {
    references?: AgentPanelReference[];
    selectedNodeIds?: string[];
    selectionCount?: number;
    focusReference?: (reference: AgentPanelReference) => void;
    attachments?: AgentChatAttachment[];
    removeAttachment?: (id: string) => void;
    retryAttachment?: (id: string) => void;
    attachmentBlockReason?: string;
    addFiles?: (files: FileList | File[] | null) => void | string[] | Promise<void | string[]>;
    prepareSubmission?: () => Promise<void>;
    /** Canvas adapter only: undo the changes belonging to one terminal run. */
    undoRun?: (runId: string) => Promise<void>;
    onEvent?: (event: AgentEvent) => void;
    headerAccessory?: ReactNode;
    targetControl?: ReactNode;
    approvalAccessory?: ReactNode;
    welcomeAccessory?: ReactNode;
    settingsExtension?: ReactNode;
    conversationFooter?: ReactNode;
    launcherNotice?: ReactNode;
    promptEditor?: ComponentType<AgentPromptEditorProps>;
    takePrefill?: () => AgentPanelPrefill | null;
    dock?: {
        available: boolean;
        active: boolean;
        width: number;
        onToggle?: () => void;
        onReset?: () => void;
        onResize?: (width: number) => void;
    };
};

export type AgentPanelAdapter = {
    configId: string;
    owner: string;
    buildRequest: (input: AgentPanelBuildRequestInput) => AgentRunRequest;
    useBindings?: () => AgentPanelBindings;
};

const adapters = new Map<string, AgentPanelAdapter>();
const listeners = new Set<() => void>();

function emit() {
    for (const listener of listeners) listener();
}

export function registerAgentPanelAdapter(businessType: string, adapter: AgentPanelAdapter) {
    adapters.set(businessType, adapter);
    emit();
    return () => {
        if (adapters.get(businessType)?.owner === adapter.owner) {
            adapters.delete(businessType);
            emit();
        }
    };
}

export function getAgentPanelAdapter(businessType: string) {
    return adapters.get(businessType) || null;
}

function subscribe(listener: () => void) {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

export function useAgentPanelAdapter(businessType: string) {
    return useSyncExternalStore(
        subscribe,
        () => getAgentPanelAdapter(businessType),
        () => null,
    );
}
