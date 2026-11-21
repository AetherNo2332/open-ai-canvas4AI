import { useEffect, useRef, useState } from "react";
import { Alert, Button, Input, Select, Space, Tag, message } from "antd";
import { AppModal } from "@/components/ui/product/app-modal";
import { createCreationPlatformClient, type GenerationRequest, type ModelSelection } from "@/sdk/creation-platform";
import { webTransport } from "@/sdk/creation-platform/http-transport";
import { buildSplitPrompt } from "@/lib/interactive/split-prompt";
import { parseSplitPayload } from "@/lib/interactive/split-parse";
import { isImportable, validateSplitRows } from "@/lib/interactive/split-validate";
import type { SplitIssue, SplitRow } from "@/lib/interactive/split-types";
import type { StoryNode } from "./story-data";
import { createDraft, markAdopted, updateDraft, type SplitDraft } from "./split-draft";
import { mapSplitToNodes } from "./split-import";
import { SplitIssues } from "./split-issues";
import { SplitTable } from "./split-table";

interface SplitDialogProps {
    open: boolean;
    projectId: string;
    projectTitle: string;
    /** 项目已有个性化节点时不允许原地导入，避免覆盖已有制作成果。 */
    hasNodes: boolean;
    onClose: () => void;
    onImport: (nodes: StoryNode[]) => void;
}

const platform = createCreationPlatformClient({ transport: webTransport });

/** 三步：贴剧本 → 校对表格 → 导入；每次拆分落一个草稿版本。 */
export function SplitDialog({ open, projectId, projectTitle, hasNodes, onClose, onImport }: SplitDialogProps) {
    const [step, setStep] = useState<"input" | "review">("input");
    const [script, setScript] = useState("");
    const [requirements, setRequirements] = useState("");
    const [models, setModels] = useState<{ selection: ModelSelection; label: string; available: boolean }[]>([]);
    const [modelIndex, setModelIndex] = useState(0);
    const [modelsError, setModelsError] = useState("");
    const [quoteCredits, setQuoteCredits] = useState<number | null>(null);
    const [running, setRunning] = useState(false);
    const [progress, setProgress] = useState(0);
    const [error, setError] = useState("");
    const [rows, setRows] = useState<SplitRow[]>([]);
    const [issues, setIssues] = useState<SplitIssue[]>([]);
    const [draft, setDraft] = useState<SplitDraft | null>(null);
    const abortRef = useRef<AbortController | null>(null);
    const taskIdRef = useRef("");

    useEffect(() => {
        if (!open) return;
        let cancelled = false;
        setModelsError("");
        void platform
            .models("text")
            .then((catalog) => {
                if (cancelled) return;
                const available = catalog.models.filter((item) => item.available);
                setModels(available);
                if (available.length === 0) setModelsError("当前账号没有可用的文本模型，请联系管理员开通");
            })
            .catch((cause: unknown) => {
                if (!cancelled) setModelsError(cause instanceof Error ? cause.message : "读取模型目录失败");
            });
        return () => {
            cancelled = true;
        };
    }, [open]);

    useEffect(() => {
        if (open) return;
        abortRef.current = null;
        taskIdRef.current = "";
        setRunning(false);
        setProgress(0);
        setStep("input");
        setError("");
        setQuoteCredits(null);
        setRows([]);
        setIssues([]);
        setDraft(null);
    }, [open]);

    const selectedModel = models[modelIndex]?.selection;
    const canGenerate = Boolean(script.trim()) && Boolean(selectedModel) && !running;
    const errorCount = issues.filter((issue) => issue.severity === "error").length;
    const warningCount = issues.filter((issue) => issue.severity === "warning").length;
    const importable = isImportable(issues);

    const generate = async () => {
        if (!selectedModel) return;
        setRunning(true);
        setError("");
        setProgress(0);
        const prompt = buildSplitPrompt({ title: projectTitle, requirements, script });
        const request: GenerationRequest = {
            contractVersion: 1,
            capability: "text",
            operation: "text_generation",
            model: selectedModel,
            prompt,
            parameters: { stream: false, maxTokens: 8192 },
            business: { application: "interactive", projectId, objectType: "story_project", objectId: projectId },
            idempotencyKey: `interactive:${projectId}:${hash(prompt)}:${Math.random().toString(36).slice(2, 8)}`,
        };
        try {
            const quote = await platform.quote(request);
            setQuoteCredits(quote.amountMicrocredits / 1_000_000);
            const { snapshot } = await platform.submit({ ...request, cost: { maxMicrocredits: quote.amountMicrocredits, quoteHash: quote.quoteHash } });
            taskIdRef.current = snapshot.taskId;
            const controller = new AbortController();
            abortRef.current = controller;
            const final = await platform.waitForTask(snapshot.taskId, { signal: controller.signal, onSnapshot: (current) => setProgress(current.progress) });
            if (final.execution !== "succeeded") throw new Error(final.error?.message || "生成失败，请重试");
            const payload = parseSplitPayload(final.result?.text ?? "");
            if (!payload) throw new Error("模型没有返回可解析的 JSON，请重试或换一个文本模型");
            const checked = validateSplitRows(payload.rows);
            const created = createDraft({ projectId, title: payload.title || projectTitle, promptHash: hash(prompt), rows: payload.rows, issues: checked, taskId: snapshot.taskId });
            setRows(payload.rows);
            setIssues(checked);
            setDraft(created);
            setStep("review");
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : "拆分失败");
        } finally {
            setRunning(false);
            abortRef.current = null;
        }
    };

    const cancelRun = () => {
        abortRef.current?.abort();
        const taskId = taskIdRef.current;
        if (taskId) void platform.cancel(taskId).catch(() => undefined);
        setRunning(false);
    };

    const changeRows = (next: SplitRow[]) => {
        const checked = validateSplitRows(next);
        setRows(next);
        setIssues(checked);
        if (draft) setDraft(updateDraft(projectId, draft.id, { rows: next, issues: checked }));
    };

    const importRows = () => {
        if (!importable) return;
        if (hasNodes) {
            message.warning("当前项目已有节点；请先在互动剧列表新建一个项目，再从新项目导入，避免覆盖已有制作成果");
            return;
        }
        const mapped = mapSplitToNodes(rows);
        if (!isImportable(mapped.issues)) {
            setIssues(mapped.issues);
            return;
        }
        if (draft) markAdopted(projectId, draft.id, mapped.nodeIdByRowKey);
        onImport(mapped.nodes);
        onClose();
    };

    const footer =
        step === "input" ? (
            <Space>
                <Button onClick={onClose} disabled={running}>
                    取消
                </Button>
                {running ? (
                    <Button danger onClick={cancelRun}>
                        停止生成（{Math.round(progress)}%）
                    </Button>
                ) : (
                    <Button type="primary" onClick={() => void generate()} disabled={!canGenerate}>
                        生成拆分
                    </Button>
                )}
            </Space>
        ) : (
            <Space>
                <Button onClick={() => setStep("input")}>返回修改剧本</Button>
                <Button onClick={onClose}>稍后处理</Button>
                <Button type="primary" onClick={importRows} disabled={!importable}>
                    导入剧情总图
                </Button>
            </Space>
        );

    return (
        <AppModal open={open} onCancel={onClose} footer={footer} width={1180} title={`AI 拆分剧本 · ${projectTitle}`} maskClosable={false}>
            <div className="split-dialog">
                {step === "input" ? (
                    <>
                        <p className="split-dialog__hint">把一段剧本贴进来，Agent 会按固定表头拆成「一行一个可制作的剧情段落」。已有分支逐字保留，AI 新增的分支会标为「建议」。</p>
                        <Input.TextArea className="split-dialog__script" autoSize={{ minRows: 10, maxRows: 18 }} value={script} placeholder="粘贴剧本原文…" onChange={(event) => setScript(event.target.value)} />
                        <Input value={requirements} placeholder="改编要求（可选）：例如保留原著结尾、加强悬念" onChange={(event) => setRequirements(event.target.value)} />
                        <Space wrap>
                            <Select className="split-dialog__model" value={modelIndex} onChange={setModelIndex} options={models.map((item, index) => ({ value: index, label: item.label }))} placeholder="选择文本模型" disabled={running} />
                            {quoteCredits !== null ? <Tag color="blue">预计 {quoteCredits.toFixed(2)} 积分</Tag> : null}
                            <Tag>单表上限 500 行</Tag>
                        </Space>
                        {modelsError ? <Alert type="warning" showIcon message={modelsError} /> : null}
                        {error ? <Alert type="error" showIcon message={error} /> : null}
                    </>
                ) : (
                    <>
                        <Space wrap className="split-dialog__stats">
                            <Tag>{rows.length} 行</Tag>
                            <Tag color="green">{rows.filter((row) => row.ending).length} 个结局</Tag>
                            <Tag color={errorCount ? "red" : "default"}>{errorCount} 个错误</Tag>
                            <Tag color={warningCount ? "gold" : "default"}>{warningCount} 个提示</Tag>
                            {draft ? <Tag color="purple">草稿 v{draft.revision}</Tag> : null}
                        </Space>
                        <SplitIssues issues={issues} onLocate={(rowKey) => document.querySelector(`[data-split-row="${rowKey}"]`)?.scrollIntoView({ block: "center" })} />
                        <SplitTable rows={rows} issues={issues} onChange={changeRows} />
                        {hasNodes ? <Alert type="info" showIcon message="当前项目已有节点：导入会被拦截。请在互动剧列表新建项目后再导入，保证已有制作成果不被覆盖。" /> : null}
                        {error ? <Alert type="error" showIcon message={error} /> : null}
                    </>
                )}
            </div>
        </AppModal>
    );
}

/** 仅用于幂等键与草稿指纹，不做安全用途。 */
function hash(value: string): string {
    let result = 5381;
    for (let index = 0; index < value.length; index += 1) result = ((result << 5) + result + value.charCodeAt(index)) >>> 0;
    return result.toString(36);
}
