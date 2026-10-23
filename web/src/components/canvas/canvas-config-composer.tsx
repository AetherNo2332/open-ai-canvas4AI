import { useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import type { CSSProperties, KeyboardEvent, MouseEvent, PointerEvent, Ref } from "react";
import { App, Button, Image } from "antd";
import { FileText, Image as ImageIcon, Music2, Pencil, Save, Sparkles, Video, X } from "lucide-react";

import { canvasThemes } from "@/lib/canvas-theme";
import { isCanvasWorkflowProvider } from "@/lib/canvas/canvas-workflow";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { generationInputMentionLabel, normalizeGenerationNodeMentionTokens, type NodeGenerationInput } from "./canvas-node-generation";
import { CanvasVideoPromptTools } from "./canvas-video-prompt-tools";
import { CanvasPresetPicker, type CanvasPromptPreset } from "./canvas-preset-picker";
import type { CanvasGenerationMode, CanvasNodeMetadata, CanvasWorkspaceMode } from "@/types/canvas";
import { useCanvasEditLease } from "./use-canvas-edit-lease";
import { CanvasEditLeaseStatus } from "./canvas-edit-lease-status";
import { acknowledgeCanvasAuthorDraftFields, captureCanvasAuthorIdentity, flushCanvasAuthorDrafts, readCanvasAuthorDraft, sameCanvasAuthorIdentity, saveCanvasAuthorDraft, type CanvasAuthorIdentity } from "@/services/canvas-author-drafts";
import { canvasEditLeaseController } from "@/services/canvas-edit-leases";
import { useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";
import { getActiveUserScope } from "@/lib/user-scope";

export type CanvasConfigComposerHandle = { save: () => Promise<string> };

/** The closed-panel generation path uses the same recovery and ACK boundary. */
export async function saveCanvasConfigComposerIntent({
    projectId,
    nodeId,
    value,
    readValue,
    onChange,
    onPersist,
}: {
    projectId: string;
    nodeId: string;
    value: string;
    readValue?: () => string;
    onChange: (value: string) => void;
    onPersist: () => Promise<void>;
}): Promise<string> {
    if (!projectId || !nodeId) throw new Error("画布或节点身份缺失，未保存提示词");
    const { scope, bootstrapState, connectionGeneration } = useCanvasSessionStore.getState();
    if (!scope && bootstrapState !== "inactive") throw new Error("画布连接尚未就绪，提示词草稿已保留");
    const identity = captureCanvasAuthorIdentity(projectId, nodeId);
    const controller = scope ? canvasEditLeaseController(scope, projectId) : null;
    await flushCanvasAuthorDrafts();
    const draft = scope ? await readCanvasAuthorDraft(scope, projectId, nodeId, "prompt") : null;
    const intent = draft ? String(draft.value.prompt ?? "") : value;
    const originalBase = draft ? String(draft.base.prompt ?? "") : value;
    const originalIdentity = draft ? draft.identity : identity;
    const check = () => {
        const current = useCanvasSessionStore.getState();
        if (current.scope !== scope || current.connectionGeneration !== connectionGeneration) throw new Error("画布连接已切换，提示词草稿已保留");
        if (scope && (getActiveUserScope() !== scope || !controller?.isCurrent())) throw new Error("编辑账号已切换，提示词草稿已保留");
        if (scope && !sameCanvasAuthorIdentity(originalIdentity, captureCanvasAuthorIdentity(projectId, nodeId))) throw new Error("节点版本已改变，旧提示词草稿已保留");
        const currentValue = readValue ? readValue() : value;
        if (currentValue !== originalBase && currentValue !== intent) throw new Error("云端提示词已变化，旧草稿已保留，请先处理冲突");
    };
    check();
    if (scope) await saveCanvasAuthorDraft(scope, projectId, nodeId, { kind: "prompt", identity: originalIdentity, base: draft?.base || { prompt: value }, value: { prompt: intent } });
    const detach = controller?.retain(nodeId);
    try {
        const persist = async () => {
            check();
            onChange(intent);
            await onPersist();
            check();
        };
        if (controller) await controller.save(nodeId, persist);
        else await persist();
        if (scope) await acknowledgeCanvasAuthorDraftFields(scope, projectId, nodeId, "prompt", { prompt: intent }, originalIdentity);
        return intent;
    } finally {
        detach?.();
    }
}

type CanvasConfigComposerProps = {
    projectId: string;
    nodeId: string;
    ref?: Ref<CanvasConfigComposerHandle>;
    value: string;
    inputs: NodeGenerationInput[];
    skillReferences?: CanvasResourceReference[];
    generationMode?: CanvasGenerationMode;
    metadata?: CanvasNodeMetadata;
    onChange: (value: string) => void;
    onPersist: () => Promise<void>;
    onMetadataChange?: (patch: Partial<CanvasNodeMetadata>) => void;
    onClose: () => void;
    workspaceMode?: CanvasWorkspaceMode;
};

type Token = { type: "text"; value: string } | { type: "reference"; nodeId: string };

type MentionState = {
    query: string;
};

type ComposerCandidate =
    | {
          kind: "input";
          input: NodeGenerationInput;
      }
    | {
          kind: "skill";
          reference: CanvasResourceReference;
      };

export const CONFIG_REFERENCE_PATTERN = /@\[node:([^\]]+)\]|@(图片|视频|音频|文本|角色|绘图)(\d+)/g;

export function CanvasConfigComposer({ projectId, nodeId, ref, value, inputs, skillReferences = [], generationMode, metadata, onChange, onPersist, onMetadataChange, onClose, workspaceMode = "professional" }: CanvasConfigComposerProps) {
    const { message } = App.useApp();
    const lease = useCanvasEditLease(projectId, nodeId, true, true);
    const theme = canvasThemes[useActiveTheme()];
    const editorRef = useRef<HTMLDivElement>(null);
    const composingRef = useRef(false);
    const [mention, setMention] = useState<MentionState | null>(null);
    const [activeIndex, setActiveIndex] = useState(0);
    const [imagePreview, setImagePreview] = useState<string | null>(null);
    const [presetOpen, setPresetOpen] = useState(false);
    const [prompt, setPrompt] = useState(value);
    const promptRef = useRef(value);
    const basePromptRef = useRef(value);
    const appliedPromptRef = useRef(value);
    const latestValueRef = useRef(value);
    latestValueRef.current = value;
    const baseIdentity = useRef<CanvasAuthorIdentity | undefined>(undefined);
    const dirtyRef = useRef(false);
    const savingRef = useRef(false);
    const generationRef = useRef(0);
    const [saving, setSaving] = useState(false);
    const [draftReady, setDraftReady] = useState(false);
    const [draftConflict, setDraftConflict] = useState(false);
    const [draftError, setDraftError] = useState("");
    const editable = draftReady && lease.canEdit && !draftConflict && !saving;
    const simpleMode = workspaceMode === "simple";
    const workflowVideoReferenceMode = generationMode === "video" && isCanvasWorkflowProvider(metadata);
    const normalizedValue = useMemo(() => normalizeGenerationNodeMentionTokens(prompt, inputs), [inputs, prompt]);
    const tokens = useMemo(() => parseComposerTokens(normalizedValue, inputs), [inputs, normalizedValue]);
    const referenceById = useMemo(() => new Map(inputs.map((input) => [input.nodeId, input])), [inputs]);
    const videoFrameOptions = useMemo(() => inputs.filter((input) => input.type === "image" && input.image).map((input) => ({ nodeId: input.nodeId, label: resourceLabel(input, inputs), title: input.title, previewUrl: input.image?.dataUrl })), [inputs]);
    const candidates = useMemo(() => {
        if (!mention) return [];
        const query = (mention.query || "").trim().toLowerCase();
        const items: ComposerCandidate[] = [...skillReferences.map((reference) => ({ kind: "skill" as const, reference })), ...inputs.map((input) => ({ kind: "input" as const, input }))];
        if (!query) return items;
        return items.filter((item) => candidateSearchText(item, inputs).toLowerCase().includes(query));
    }, [inputs, mention, skillReferences]);

    useEffect(() => {
        const generation = ++generationRef.current;
        const scope = lease.scope;
        basePromptRef.current = value;
        appliedPromptRef.current = value;
        baseIdentity.current = captureCanvasAuthorIdentity(projectId, nodeId);
        promptRef.current = value;
        setPrompt(value);
        dirtyRef.current = false;
        setDraftReady(false);
        setDraftConflict(false);
        setDraftError("");
        if (!scope) {
            setDraftReady(true);
            return;
        }
        void readCanvasAuthorDraft(scope, projectId, nodeId, "prompt")
            .then((draft) => {
                if (generation !== generationRef.current || getActiveUserScope() !== scope) return;
                if (draft) {
                    const currentIdentity = baseIdentity.current;
                    baseIdentity.current = draft.identity;
                    basePromptRef.current = String(draft.base.prompt ?? "");
                    promptRef.current = String(draft.value.prompt ?? "");
                    setPrompt(promptRef.current);
                    dirtyRef.current = true;
                    setDraftConflict(!sameCanvasAuthorIdentity(draft.identity, currentIdentity) || (basePromptRef.current !== latestValueRef.current && promptRef.current !== latestValueRef.current));
                }
                setDraftReady(true);
            })
            .catch(() => {
                if (generation === generationRef.current) setDraftError("本地提示词草稿读取失败，请保留当前窗口");
            });
        return () => {
            generationRef.current++;
        };
    }, [projectId, nodeId, lease.scope, lease.controller]);

    useEffect(() => {
        if (dirtyRef.current) {
            if (value !== basePromptRef.current && value !== promptRef.current && value !== appliedPromptRef.current) setDraftConflict(true);
            return;
        }
        basePromptRef.current = value;
        promptRef.current = value;
        setPrompt(value);
    }, [value]);

    useEffect(() => {
        if (document.activeElement === editorRef.current) return;
        const editor = editorRef.current;
        if (!editor) return;
        editor.textContent = "";
        tokens.forEach((token) => {
            if (token.type === "text") {
                editor.append(document.createTextNode(token.value));
                return;
            }
            const input = referenceById.get(token.nodeId);
            if (input) editor.append(createReferenceChip(input, inputs, theme, setImagePreview));
        });
    }, [inputs, normalizedValue, referenceById, theme, tokens]);

    const remember = async (intent: string) => {
        if (!lease.scope) return;
        await saveCanvasAuthorDraft(lease.scope, projectId, nodeId, { kind: "prompt", identity: baseIdentity.current, base: { prompt: basePromptRef.current }, value: { prompt: intent } });
    };
    const canMutate = () => !savingRef.current && editable && (!lease.scope || (getActiveUserScope() === lease.scope && Boolean(lease.controller?.isCurrent())));
    const updatePrompt = (intent: string) => {
        if (!canMutate()) return;
        promptRef.current = intent;
        setPrompt(intent);
        dirtyRef.current = true;
        void remember(intent).catch(() => setDraftError("本地提示词草稿保存失败，请保持窗口打开并复制内容"));
    };
    const save = async () => {
        if (savingRef.current) throw new Error("正在确认上一份提示词，请等待保存结果");
        if (!draftReady || draftConflict) throw new Error("提示词基线尚未确认，旧草稿已保留");
        if (lease.isRemote && !sameCanvasAuthorIdentity(baseIdentity.current, captureCanvasAuthorIdentity(projectId, nodeId))) {
            setDraftConflict(true);
            throw new Error("节点版本已改变，旧提示词草稿已保留");
        }
        const intent = promptRef.current;
        const generation = generationRef.current;
        savingRef.current = true;
        setSaving(true);
        closeMention();
        setPresetOpen(false);
        try {
            await remember(intent);
            if (generation !== generationRef.current || (lease.scope && (getActiveUserScope() !== lease.scope || !lease.controller?.isCurrent()))) throw new Error("编辑窗口或账号已切换，提示词草稿已保留");
            const acknowledged = await saveCanvasConfigComposerIntent({
                projectId,
                nodeId,
                value: lease.scope ? latestValueRef.current : intent,
                readValue: lease.scope ? () => latestValueRef.current : undefined,
                onChange: (text) => {
                    appliedPromptRef.current = text;
                    onChange(text);
                },
                onPersist,
            });
            if (generation !== generationRef.current) throw new Error("编辑窗口已切换，未启动生成");
            basePromptRef.current = acknowledged;
            dirtyRef.current = false;
            return acknowledged;
        } finally {
            savingRef.current = false;
            if (generation === generationRef.current) setSaving(false);
        }
    };
    useImperativeHandle(ref, () => ({ save }));
    const saveWithFeedback = async (closeAfter = false) => {
        try {
            await save();
            if (closeAfter) onClose();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "提示词尚未确认保存，草稿已保留");
        }
    };

    const syncFromEditor = () => {
        const editor = editorRef.current;
        if (!editor || !canMutate()) return;
        const next = serializeEditor(editor);
        updatePrompt(next);
        syncMention();
    };

    const syncMention = () => {
        const text = textBeforeCaret();
        const match = /@([^\s@]*)$/.exec(text);
        if (!match || (!inputs.length && !skillReferences.length)) {
            closeMention();
            return;
        }
        const nextQuery = match[1] || "";
        if (mention?.query !== nextQuery) {
            setMention({ query: nextQuery });
            setActiveIndex(0);
        }
    };

    const closeMention = () => {
        setMention(null);
        setActiveIndex(0);
    };

    const insertCandidate = (candidate: ComposerCandidate) => {
        const editor = editorRef.current;
        if (!editor || !canMutate()) return;
        removeActiveMention();
        const space = document.createTextNode(" ");
        const node = candidate.kind === "skill" ? document.createTextNode(`@${candidate.reference.label}`) : createReferenceChip(candidate.input, inputs, theme, setImagePreview);
        const selection = window.getSelection();
        const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
        if (range) {
            range.insertNode(space);
            range.insertNode(node);
            range.setStartAfter(space);
            range.collapse(true);
            selection?.removeAllRanges();
            selection?.addRange(range);
        } else {
            editor.append(node, space);
            placeCaretAtEnd(editor);
        }
        closeMention();
        updatePrompt(serializeEditor(editor));
    };

    const stopCanvasInteraction = (event: PointerEvent | MouseEvent) => event.stopPropagation();

    const insertPreset = (preset: CanvasPromptPreset) => {
        const editor = editorRef.current;
        if (!editor || !canMutate()) return;
        removeActiveSlash(editor);
        const selection = window.getSelection();
        const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
        const text = document.createTextNode(`${preset.prompt} `);
        if (range && editor.contains(range.commonAncestorContainer)) {
            range.insertNode(text);
            range.setStartAfter(text);
            range.collapse(true);
            selection?.removeAllRanges();
            selection?.addRange(range);
        } else {
            editor.append(text);
            placeCaretAtEnd(editor);
        }
        updatePrompt(serializeEditor(editor));
    };

    return (
        <div
            data-canvas-no-zoom
            className="canvas-config-composer aceternity-floating-panel rounded-xl p-4 backdrop-blur-2xl"
            style={{ background: theme.spatial.elevated, color: theme.node.text }}
            onMouseDown={stopCanvasInteraction}
            onPointerDown={stopCanvasInteraction}
            onWheel={(event) => event.stopPropagation()}
        >
            <div className="mb-3 flex items-center justify-between gap-3">
                <div className="flex min-w-0 items-baseline gap-2">
                    <div className="shrink-0 text-xs font-semibold">{simpleMode ? "快速生成" : "组装提示词"}</div>
                    <div className="truncate text-[var(--fs-label)] opacity-55">{simpleMode ? "已连接素材会自动带入" : workflowVideoReferenceMode ? "已连接媒体会按工作流字段顺序自动带入" : "@ 引用已连接素材或已激活技能，发送前自动组装"}</div>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                    {simpleMode ? null : (
                        <CanvasPresetPicker
                            mode={generationMode || "image"}
                            skillReferences={skillReferences}
                            open={presetOpen && editable}
                            onOpenChange={(open) => {
                                if (canMutate()) setPresetOpen(open);
                            }}
                            onSelect={insertPreset}
                        />
                    )}
                    <Button size="small" type="text" icon={<Save className="size-3.5" />} loading={saving} disabled={!draftReady || draftConflict} onClick={() => void saveWithFeedback()}>
                        保存
                    </Button>
                    <Button
                        size="small"
                        type="text"
                        className="!h-7 !w-7 !min-w-7 !p-0"
                        aria-label="关闭组装提示词"
                        disabled={saving}
                        icon={<X className="size-3.5" />}
                        onClick={() => {
                            if (!dirtyRef.current && !savingRef.current) onClose();
                            else void saveWithFeedback(true);
                        }}
                    />
                </div>
            </div>
            <div className="mb-3 space-y-1">
                <CanvasEditLeaseStatus
                    phase={lease.phase}
                    message={draftError || lease.message}
                    onRetry={() => {
                        if (!savingRef.current && !draftConflict) void lease.begin().catch(() => {});
                    }}
                />
                {draftConflict ? (
                    <div role="alert" className="text-xs text-muted-foreground">
                        云端内容或节点版本与旧草稿不同。请复制下方提示词保留原意，再处理冲突；当前草稿不会覆盖云端。
                    </div>
                ) : null}
            </div>
            {generationMode === "video" && onMetadataChange && !simpleMode ? (
                <div className="mb-3 rounded-lg px-2 py-2" style={{ background: theme.node.fill }}>
                    <CanvasVideoPromptTools
                        metadata={metadata}
                        frameOptions={videoFrameOptions}
                        referenceMode={workflowVideoReferenceMode ? "all" : "frames"}
                        referenceSummary={{
                            imageCount: inputs.filter((input) => input.type === "image" || input.type === "character").length,
                            videoCount: inputs.filter((input) => input.type === "video").length,
                            audioCount: inputs.filter((input) => input.type === "audio").length,
                        }}
                        onMetadataChange={(patch) => {
                            if (canMutate()) {
                                dirtyRef.current = true;
                                onMetadataChange(patch);
                            }
                        }}
                    />
                </div>
            ) : null}
            <div className="canvas-config-composer-editor relative rounded-lg" style={{ background: theme.node.fill }}>
                {!prompt.trim() ? (
                    <div className="pointer-events-none absolute left-4 top-3 text-sm leading-7" style={{ color: theme.node.placeholder }}>
                        输入提示词，按 @ 引用连接素材或技能
                    </div>
                ) : null}
                <div
                    ref={editorRef}
                    contentEditable={editable}
                    role="textbox"
                    aria-label="Config 节点提示词"
                    aria-readonly={!editable}
                    suppressContentEditableWarning
                    className="thin-scrollbar min-h-32 max-h-[min(42vh,360px)] w-full overflow-y-auto whitespace-pre-wrap break-words px-4 py-3 text-sm leading-7 outline-none"
                    style={{ color: theme.node.text }}
                    onFocus={() => {
                        if (!savingRef.current && !draftConflict) void lease.begin().catch(() => {});
                    }}
                    onInput={() => {
                        if (!composingRef.current) syncFromEditor();
                    }}
                    onCompositionStart={() => {
                        composingRef.current = true;
                    }}
                    onCompositionEnd={() => {
                        composingRef.current = false;
                        syncFromEditor();
                    }}
                    onKeyDown={(event: KeyboardEvent<HTMLDivElement>) => {
                        event.stopPropagation();
                        if (!canMutate()) return;
                        if (event.key === "/" && !mention) window.setTimeout(() => setPresetOpen(true));
                        if (mention && candidates.length) {
                            if (event.key === "ArrowDown") {
                                event.preventDefault();
                                setActiveIndex((index) => (index + 1) % candidates.length);
                                return;
                            }
                            if (event.key === "ArrowUp") {
                                event.preventDefault();
                                setActiveIndex((index) => (index - 1 + candidates.length) % candidates.length);
                                return;
                            }
                            if (event.key === "Enter") {
                                event.preventDefault();
                                insertCandidate(candidates[Math.min(activeIndex, candidates.length - 1)]);
                                return;
                            }
                            if (event.key === "Escape") {
                                event.preventDefault();
                                closeMention();
                                return;
                            }
                        }
                        if ((event.key === "Backspace" || event.key === "Delete") && deleteAdjacentReference(event.key)) {
                            event.preventDefault();
                            requestAnimationFrame(syncFromEditor);
                            return;
                        }
                        requestAnimationFrame(syncMention);
                    }}
                    onBlur={() => window.setTimeout(closeMention, 120)}
                />
                {mention && candidates.length ? <MentionMenu candidates={candidates} allInputs={inputs} activeIndex={Math.min(activeIndex, candidates.length - 1)} theme={theme} onSelect={insertCandidate} /> : null}
            </div>
            {imagePreview ? <Image src={imagePreview} alt="引用图片预览" style={{ display: "none" }} preview={{ visible: true, src: imagePreview, onVisibleChange: (visible) => !visible && setImagePreview(null) }} /> : null}
        </div>
    );
}

function removeActiveSlash(editor: HTMLDivElement) {
    const selection = window.getSelection();
    const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
    if (!range || !editor.contains(range.commonAncestorContainer)) return;
    const node = range.startContainer;
    if (node.nodeType !== Node.TEXT_NODE) return;
    const value = node.textContent || "";
    const before = value.slice(0, range.startOffset);
    const match = /(^|\s)\/[\p{L}\p{N}_-]*$/u.exec(before);
    if (!match) return;
    node.textContent = `${before.slice(0, match.index)}${match[1]}${value.slice(range.startOffset)}`;
    range.setStart(node, match.index + match[1].length);
    range.collapse(true);
    selection?.removeAllRanges();
    selection?.addRange(range);
}

function MentionMenu({
    candidates,
    allInputs,
    activeIndex,
    theme,
    onSelect,
}: {
    candidates: ComposerCandidate[];
    allInputs: NodeGenerationInput[];
    activeIndex: number;
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    onSelect: (candidate: ComposerCandidate) => void;
}) {
    const selectedRef = useRef(false);
    const activeItemRef = useRef<HTMLButtonElement | null>(null);

    useEffect(() => {
        activeItemRef.current?.scrollIntoView({ block: "nearest" });
    }, [activeIndex, candidates]);

    const selectInput = (candidate: ComposerCandidate) => {
        if (selectedRef.current) return;
        selectedRef.current = true;
        onSelect(candidate);
    };

    return (
        <div className="aceternity-floating-panel absolute left-2 top-[calc(100%+6px)] z-[var(--z-modal)] max-h-56 w-64 overflow-y-auto rounded-lg border p-1" style={{ background: theme.spatial.elevated, borderColor: theme.toolbar.border }}>
            {candidates.map((candidate, index) => (
                <button
                    key={candidate.kind === "skill" ? candidate.reference.id : candidate.input.nodeId}
                    ref={index === activeIndex ? activeItemRef : undefined}
                    type="button"
                    className="flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition"
                    style={{ background: index === activeIndex ? theme.toolbar.activeBg : "transparent", color: index === activeIndex ? theme.toolbar.activeText : theme.node.text }}
                    onMouseDown={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                        selectInput(candidate);
                    }}
                >
                    <ResourcePreview candidate={candidate} />
                    <span className="min-w-0 flex-1">
                        <span className="block font-medium">{candidate.kind === "skill" ? candidate.reference.label : resourceLabel(candidate.input, allInputs)}</span>
                        {candidate.kind !== "skill" ? <span className="block truncate opacity-65">{candidate.input.text || candidate.input.title}</span> : null}
                    </span>
                </button>
            ))}
        </div>
    );
}

function ResourcePreview({ candidate }: { candidate: ComposerCandidate }) {
    if (candidate.kind === "skill") {
        return (
            <span className="grid size-9 shrink-0 place-items-center rounded-md bg-cyan-500/12 text-cyan-600 dark:text-cyan-200">
                <Sparkles className="size-4" />
            </span>
        );
    }
    const input = candidate.input;
    if (input.sourceKind === "drawing") {
        return (
            <span className="grid size-9 shrink-0 place-items-center rounded-md bg-black/10">
                <Pencil className="size-4" />
            </span>
        );
    }
    if (input.type === "image" && input.image) return <img src={input.image.dataUrl} alt="" className="size-9 rounded-md object-cover" />;
    if (input.type === "video" && input.previewUrl) return <img src={input.previewUrl} alt="" className="size-9 rounded-md bg-black object-cover" loading="lazy" decoding="async" />;
    const Icon = input.type === "audio" ? Music2 : input.type === "video" ? Video : input.type === "image" ? ImageIcon : FileText;
    return (
        <span className="grid size-9 shrink-0 place-items-center rounded-md bg-black/10">
            <Icon className="size-4" />
        </span>
    );
}

function candidateSearchText(candidate: ComposerCandidate, inputs: NodeGenerationInput[]) {
    if (candidate.kind === "skill") return `技能 ${candidate.reference.label} ${candidate.reference.title} ${candidate.reference.text || ""}`;
    return `${resourceLabel(candidate.input, inputs)} ${candidate.input.title} ${candidate.input.text || ""}`;
}

function createReferenceChip(input: NodeGenerationInput, inputs: NodeGenerationInput[], theme: (typeof canvasThemes)[keyof typeof canvasThemes], onImagePreview: (url: string) => void) {
    const wrapper = document.createElement("span");
    wrapper.contentEditable = "false";
    wrapper.dataset.referenceNodeId = input.nodeId;
    wrapper.dataset.referenceToken = `@${generationInputMentionLabel(input, inputs)}`;
    wrapper.className = "mx-px inline-flex h-7 max-w-40 items-center justify-center overflow-hidden rounded-md border px-1 text-xs leading-none align-middle";
    Object.assign(wrapper.style, chipStyle(theme));
    const previewUrl = input.type === "image" && input.image && input.sourceKind !== "drawing" ? input.image.dataUrl : input.type === "video" ? input.previewUrl : "";
    if (previewUrl) {
        const image = document.createElement("img");
        image.src = previewUrl;
        image.alt = input.title;
        image.className = "size-6 rounded object-cover";
        wrapper.className = "mx-px inline-flex size-6 items-center justify-center overflow-hidden rounded align-middle";
        wrapper.appendChild(image);
        wrapper.addEventListener("click", (event) => {
            event.preventDefault();
            event.stopPropagation();
            onImagePreview(previewUrl);
        });
    } else {
        wrapper.title = input.sourceKind === "drawing" ? resourceLabel(input, inputs) : input.text || input.title;
        const text = document.createElement("span");
        text.className = "block truncate";
        text.textContent = input.sourceKind === "drawing" ? resourceLabel(input, inputs) : input.type === "text" ? input.text || input.title : input.title;
        wrapper.appendChild(text);
    }
    return wrapper;
}

function serializeEditor(editor: HTMLElement) {
    return serializeNodes(editor.childNodes).replace(/\uFEFF/g, "");
}

function serializeNodes(nodes: NodeListOf<ChildNode>) {
    let result = "";
    nodes.forEach((node) => {
        if (node.nodeType === Node.TEXT_NODE) result += node.textContent || "";
        if (!(node instanceof HTMLElement)) return;
        const referenceToken = node.dataset.referenceToken;
        if (referenceToken) result += referenceToken;
        else if (node.tagName === "BR") result += "\n";
        else result += serializeNodes(node.childNodes);
    });
    return result;
}

function removeActiveMention() {
    const selection = window.getSelection();
    if (!selection?.rangeCount) return;
    const range = selection.getRangeAt(0);
    const text = textBeforeCaret();
    const match = /@([^\s@]*)$/.exec(text);
    if (!match) return;
    range.setStart(range.startContainer, Math.max(0, range.startOffset - (match[1] || "").length - 1));
    range.deleteContents();
}

function deleteAdjacentReference(key: string) {
    const selection = window.getSelection();
    if (!selection?.rangeCount || !selection.isCollapsed) return false;
    const range = selection.getRangeAt(0);
    const target = adjacentReferenceNode(range, key);
    if (!target) return false;
    const nextCaretNode = document.createTextNode("");
    target.replaceWith(nextCaretNode);
    range.setStart(nextCaretNode, 0);
    range.collapse(true);
    selection.removeAllRanges();
    selection.addRange(range);
    return true;
}

function adjacentReferenceNode(range: Range, key: string) {
    const container = range.startContainer;
    const offset = range.startOffset;
    const previous = key === "Backspace";
    if (container.nodeType === Node.TEXT_NODE) {
        const text = container.textContent || "";
        if ((previous && offset > 0) || (!previous && offset < text.length)) return null;
        return findReferenceSibling(container, previous);
    }
    const children = Array.from(container.childNodes);
    return findReferenceSibling(children[previous ? offset - 1 : offset] || container, previous, true);
}

function findReferenceSibling(node: Node, previous: boolean, includeSelf = false): HTMLElement | null {
    let current: Node | null = includeSelf ? node : previous ? node.previousSibling : node.nextSibling;
    while (current && current.nodeType === Node.TEXT_NODE && !(current.textContent || "").trim()) current = previous ? current.previousSibling : current.nextSibling;
    return current instanceof HTMLElement && current.dataset.referenceNodeId ? current : null;
}

function textBeforeCaret() {
    const selection = window.getSelection();
    if (!selection?.rangeCount) return "";
    const range = selection.getRangeAt(0).cloneRange();
    const editor = closestEditor(range.startContainer);
    if (!editor) return "";
    range.setStart(editor, 0);
    return range.toString();
}

function closestEditor(node: Node) {
    const element = node instanceof Element ? node : node.parentElement;
    return element?.closest("[contenteditable='true']") || null;
}

function placeCaretAtEnd(element: HTMLElement) {
    const range = document.createRange();
    range.selectNodeContents(element);
    range.collapse(false);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
}

function parseComposerTokens(value: string, inputs: NodeGenerationInput[]): Token[] {
    const tokens: Token[] = [];
    const inputByLabel = new Map(inputs.map((input) => [generationInputMentionLabel(input, inputs), input]));
    const inputByNodeId = new Map(inputs.map((input) => [input.nodeId, input]));
    let lastIndex = 0;
    for (const match of value.matchAll(CONFIG_REFERENCE_PATTERN)) {
        if (match.index === undefined) continue;
        const input = match[1] ? inputByNodeId.get(match[1]) : inputByLabel.get(`${match[2]}${match[3]}`);
        const end = match.index + match[0].length;
        if (!input || (!match[1] && !hasMentionBoundary(value, end))) continue;
        if (match.index > lastIndex) tokens.push({ type: "text", value: value.slice(lastIndex, match.index) });
        tokens.push({ type: "reference", nodeId: input.nodeId });
        lastIndex = end;
    }
    if (lastIndex < value.length) tokens.push({ type: "text", value: value.slice(lastIndex) });
    return tokens;
}

function resourceLabel(input: NodeGenerationInput, inputs: NodeGenerationInput[]) {
    return generationInputMentionLabel(input, inputs);
}

function hasMentionBoundary(value: string, index: number) {
    const char = value[index];
    return !char || /\s|[,.!?;:，。！？；：、)\]}】）]/.test(char);
}

function chipStyle(theme: (typeof canvasThemes)[keyof typeof canvasThemes]): CSSProperties {
    return { background: theme.toolbar.panel, borderColor: theme.node.stroke, color: theme.node.text };
}
