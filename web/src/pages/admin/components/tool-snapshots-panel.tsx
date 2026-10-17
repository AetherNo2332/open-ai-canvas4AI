import { Alert, Button, Input, Select, Space, Table } from "antd";
import { useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { AppDrawer } from "@/components/ui/product/app-drawer";
import { AppModal } from "@/components/ui/product/app-modal";
import {
    compareToolSnapshots,
    getSnapshotHealth,
    getToolSnapshot,
    listToolSnapshots,
    recheckToolSnapshots,
    setWorkerDraining,
    type SnapshotComparison,
    type SnapshotDetail,
    type SnapshotHealth,
    type SnapshotSummary,
    type SnapshotWorker,
    type ToolDifference,
} from "@/services/api/tool-snapshots";
import { diagnosticExport, healthPresentation, runDrainOperation, startHealthPolling, snapshotListQuery, snapshotSearchParams } from "./tool-snapshot-state";
import "./tool-snapshots-panel.css";
import { SnapshotJson } from "./snapshot-json";
import { SnapshotReferences } from "./snapshot-references";

const statusLabels: Record<string, string> = { matched: "一致", mismatch: "存在差异", unknown: "未知", offline: "离线", invalid: "制品无效" };
const sourceLabels: Record<string, string> = { backend: "后端", worker: "Worker", task: "任务", run: "任务" };
const errorText = (error: unknown) => (error instanceof Error ? error.message : "请求失败");
function download(value: unknown) {
    const url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "tool-snapshot-diagnostics.json";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export default function ToolSnapshotsPanel() {
    const [params, setParams] = useSearchParams();
    const opened = params.get("toolSnapshots") === "1";
    const selectedId = params.get("snapshot") || "";
    const left = params.get("snapshotLeft") || "";
    const right = params.get("snapshotRight") || "";
    const source = params.get("snapshotSource") || "";
    const status = params.get("snapshotStatus") || "";
    const toolFilter = params.get("snapshotTool") || "";
    const version = params.get("snapshotVersion") || "";
    const listQuery = snapshotListQuery(params);
    const page = listQuery.page;
    const [health, setHealth] = useState<SnapshotHealth | null>(null);
    const [healthError, setHealthError] = useState("");
    const [snapshots, setSnapshots] = useState<SnapshotSummary[]>([]);
    const [total, setTotal] = useState(0);
    const [detail, setDetail] = useState<SnapshotDetail | null>(null);
    const [comparison, setComparison] = useState<SnapshotComparison | null>(null);
    const [readError, setReadError] = useState("");
    const [loading, setLoading] = useState(false);
    const [toolName, setToolName] = useState("");
    const [toolSearch, setToolSearch] = useState("");
    const [checking, setChecking] = useState(false);
    const [operation, setOperation] = useState<{ text: string; failed: boolean } | null>(null);
    const [target, setTarget] = useState<SnapshotWorker | null>(null);
    const [reason, setReason] = useState("");
    const [saving, setSaving] = useState(false);
    const manualRequest = useRef<AbortController | null>(null);
    const latestDetection = useRef(0);
    const updateHealth = useCallback((value: SnapshotHealth | null, error: string) => {
        const detectedAt = Date.parse(value?.generatedAt ?? "");
        if (Number.isFinite(detectedAt)) {
            if (detectedAt < latestDetection.current) return;
            latestDetection.current = detectedAt;
        }
        setHealth(value);
        setHealthError(error);
    }, []);
    const refreshHealth = useCallback(async () => {
        const value = await getSnapshotHealth();
        updateHealth(value, "");
        return value;
    }, [updateHealth]);
    useEffect(
        () =>
            startHealthPolling({
                visible: () => document.visibilityState === "visible",
                listen: (fn) => {
                    document.addEventListener("visibilitychange", fn);
                    return () => document.removeEventListener("visibilitychange", fn);
                },
                interval: (fn, ms) => {
                    const id = window.setInterval(fn, ms);
                    return () => window.clearInterval(id);
                },
                load: getSnapshotHealth,
                update: updateHealth,
            }),
        [updateHealth],
    );
    useEffect(
        () => () => {
            manualRequest.current?.abort();
        },
        [],
    );
    const query = (values: Record<string, string>) => setParams((previous) => snapshotSearchParams(previous, values));
    useEffect(() => {
        if (!opened) return;
        const controller = new AbortController();
        setLoading(true);
        setReadError("");
        listToolSnapshots(listQuery, controller.signal)
            .then((data) => {
                if (!controller.signal.aborted) {
                    setSnapshots(data.snapshots ?? []);
                    setTotal(data.total);
                }
            })
            .catch((error) => {
                if (!controller.signal.aborted) {
                    setSnapshots([]);
                    setReadError(errorText(error));
                }
            })
            .finally(() => {
                if (!controller.signal.aborted) setLoading(false);
            });
        return () => controller.abort();
    }, [opened, page, source, toolFilter, version]);
    useEffect(() => {
        setDetail(null);
        setToolName("");
        setToolSearch("");
        if (!opened || !selectedId) return;
        const controller = new AbortController();
        setReadError("");
        getToolSnapshot(selectedId, controller.signal)
            .then((data) => {
                if (!controller.signal.aborted) {
                    setDetail(data);
                    setToolName(data.artifact?.tools?.[0]?.function.name ?? "");
                }
            })
            .catch((error) => {
                if (!controller.signal.aborted) setReadError(errorText(error));
            });
        return () => controller.abort();
    }, [opened, selectedId]);
    useEffect(() => {
        setComparison(null);
        if (!opened || !left || !right) return;
        const controller = new AbortController();
        setReadError("");
        compareToolSnapshots(left, right, controller.signal)
            .then((value) => {
                if (!controller.signal.aborted) setComparison({ ...value, differences: value.differences ?? [] });
            })
            .catch((error) => {
                if (!controller.signal.aborted) setReadError(errorText(error));
            });
        return () => controller.abort();
    }, [opened, left, right]);
    const presentation = healthPresentation(health, healthError);
    const workers = health?.workers ?? [];
    const rows = snapshots.filter((s) => !status || workers.some((w) => w.snapshotId === s.id && w.status === status));
    const tools = detail?.artifact?.tools ?? [];
    const selectedTool = tools.find((t) => t.function.name === toolName);
    const check = async () => {
        manualRequest.current?.abort();
        const controller = new AbortController();
        manualRequest.current = controller;
        setChecking(true);
        setOperation(null);
        try {
            const result = await recheckToolSnapshots(controller.signal);
            if (!controller.signal.aborted) {
                updateHealth(result, "");
                setOperation({ text: "重新检测已完成；基于进程已上报的制品，未重载或重启。", failed: false });
            }
        } catch (error) {
            if (!controller.signal.aborted) {
                setHealthError(errorText(error));
                setOperation({ text: `重新检测失败：${errorText(error)}`, failed: true });
            }
        } finally {
            if (!controller.signal.aborted) setChecking(false);
        }
    };
    const drain = async () => {
        if (!target) return;
        setSaving(true);
        setOperation(null);
        try {
            await runDrainOperation(target, !target.manualDraining, reason, setWorkerDraining, refreshHealth);
            setOperation({ text: `${target.instanceId}：${target.manualDraining ? "已解除手工排空；自动兼容性限制仍生效" : "已排空，停止领取新任务；现有任务继续执行"}`, failed: false });
            setTarget(null);
            setReason("");
        } catch (error) {
            setOperation({ text: `操作未确认成功：${errorText(error)}。已重新读取实例状态；版本冲突时请核对后重试。`, failed: true });
        } finally {
            setSaving(false);
        }
    };
    return (
        <section className="tool-snapshots" aria-labelledby="tool-snapshot-heading">
            <div className="tool-snapshots-heading">
                <div>
                    <h2 id="tool-snapshot-heading">工具快照状态</h2>
                    <p>当前系统检测 · 独立于日期、用户、渠道和模型筛选</p>
                </div>
                <Space wrap>
                    <Button loading={checking} onClick={() => void check()}>
                        重新检测
                    </Button>
                    <Button onClick={() => query({ toolSnapshots: "1" })}>管理工具快照</Button>
                </Space>
            </div>
            <Alert
                type={presentation.type}
                showIcon
                title={presentation.title}
                description={
                    <>
                        <p>
                            {healthError ||
                                `${workers.filter((w) => w.online && w.status !== "matched").length} 个在线异常 worker · ${new Set(workers.flatMap((w) => (w.differences ?? []).map((d) => d.toolName))).size} 个差异工具 · ${health?.affectedRuns ?? "未知"} 个受影响任务`}
                        </p>
                        <p>最近检测：{health?.generatedAt || "尚未完成"}。参数定义内的说明文字也采用严格比较。{health?.truncated && "任务检测范围已截断，显示数量为下限。"}</p>
                        {(health?.alerts ?? []).map((a, index) => (
                            <p key={`${a.code}-${index}`}>
                                {a.title}：{a.description}
                            </p>
                        ))}
                    </>
                }
            />
            {operation && <Alert role="status" type={operation.failed ? "error" : "info"} title={operation.text} />}
            <AppDrawer title="工具快照管理" open={opened} width="min(1120px, 100vw)" onClose={() => query({ toolSnapshots: "" })}>
                <div className="tool-snapshots-content">
                    <p>当前状态与历史快照分开呈现。快照不可修改；排空和解除操作保留服务端审计。</p>
                    {readError && <Alert type="error" showIcon title={readError} />}
                    {operation && <Alert role="status" type={operation.failed ? "error" : "info"} title={operation.text} />}
                    <Space wrap>
                        <Button loading={checking} onClick={() => void check()}>
                            重新检测
                        </Button>
                        <Button onClick={() => download(diagnosticExport(health, detail ? [detail] : []))}>导出当前快照诊断元数据</Button>
                    </Space>
                    <div className="tool-snapshots-filters">
                        <Select
                            aria-label="快照来源"
                            placeholder="全部来源"
                            allowClear
                            value={source || undefined}
                            onChange={(v) => query({ snapshotSource: v || "" })}
                            options={["backend", "worker", "task"].map((v) => ({ value: v, label: sourceLabels[v] }))}
                        />
                        <Select
                            aria-label="当前页 worker 检测状态"
                            placeholder="当前页全部 worker 状态"
                            allowClear
                            value={status || undefined}
                            onChange={(v) => query({ snapshotStatus: v || "" })}
                            options={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))}
                        />
                        <Input aria-label="版本筛选" placeholder="提交 / schema 版本" value={version} onChange={(e) => query({ snapshotVersion: e.target.value })} />
                        <Input aria-label="按工具名筛选快照" placeholder="完整工具名（精确匹配）" value={toolFilter} onChange={(e) => query({ snapshotTool: e.target.value })} />
                    </div>
                    <p>
                        服务端筛选共 {total} 份快照；第 {page} 页加载 {snapshots.length} 份，当前页状态筛选后 {rows.length} 份。翻页可查看更早快照。
                    </p>
                    <Table<SnapshotSummary>
                        size="small"
                        rowKey="id"
                        loading={loading}
                        dataSource={rows}
                        scroll={{ x: 800 }}
                        pagination={{ current: page, pageSize: 20, total, showSizeChanger: false, onChange: (nextPage) => query({ snapshotPage: String(nextPage) }) }}
                        columns={[
                            {
                                title: "快照 / 来源",
                                render: (_, s) => (
                                    <>
                                        <Button type="link" onClick={() => query({ snapshot: s.id })}>
                                            {s.id}
                                        </Button>
                                        <div>{sourceLabels[s.source] || s.source}</div>
                                    </>
                                ),
                            },
                            {
                                title: "版本",
                                render: (_, s) => (
                                    <>
                                        <div>{s.buildCommit || "未知提交"}</div>
                                        <div>{s.schemaVersion || "未知 schema"}</div>
                                    </>
                                ),
                            },
                            { title: "工具数", dataIndex: "toolCount" },
                            { title: "创建时间", dataIndex: "createdAt" },
                            {
                                title: "当前引用 / 状态",
                                render: (_, s) => (
                                    <>
                                        {workers
                                            .filter((w) => w.snapshotId === s.id)
                                            .map((w) => (
                                                <div key={w.instanceId}>
                                                    {w.instanceId} · {statusLabels[w.status] || "未知"}
                                                </div>
                                            ))}
                                        {!workers.some((w) => w.snapshotId === s.id) && "无当前 worker 引用"}
                                    </>
                                ),
                            },
                            {
                                title: "比较",
                                render: (_, s) => (
                                    <Space>
                                        <Button onClick={() => query({ snapshotLeft: s.id })}>设为左侧</Button>
                                        <Button onClick={() => query({ snapshotRight: s.id })}>设为右侧</Button>
                                    </Space>
                                ),
                            },
                        ]}
                    />
                    <h3>当前 worker 与执行影响</h3>
                    <Table<SnapshotWorker>
                        size="small"
                        rowKey="instanceId"
                        dataSource={workers}
                        scroll={{ x: 700 }}
                        pagination={{ pageSize: 10 }}
                        columns={[
                            { title: "实例", dataIndex: "instanceId" },
                            {
                                title: "状态",
                                render: (_, w) => (
                                    <>
                                        {statusLabels[w.status] || "未知"}
                                        {!w.online && " · 离线"}
                                        {w.manualDraining && " · 手工排空"}
                                        {w.artifactFileState === "changed" && <div>制品文件元数据已变化，内存快照未重载</div>}
                                        {w.artifactFileState === "unavailable" && <div>制品文件当前不可读，仍使用已加载快照</div>}
                                    </>
                                ),
                            },
                            { title: "受影响任务", dataIndex: "affectedRuns" },
                            { title: "尚未结束", render: (_, w) => w.unfinishedRuns ?? "未知" },
                            { title: "心跳", dataIndex: "lastHeartbeatAt" },
                            {
                                title: "操作",
                                render: (_, w) => (
                                    <Space wrap>
                                        <Button disabled={!w.snapshotId || !health?.backend?.id} onClick={() => query({ snapshotLeft: health?.backend?.id || "", snapshotRight: w.snapshotId, snapshot: w.snapshotId })}>
                                            与后端比较
                                        </Button>
                                        <Button
                                            disabled={!Number.isFinite(w.revision) || w.revision < 1}
                                            onClick={() => {
                                                setTarget(w);
                                                setReason("");
                                            }}
                                        >
                                            {w.manualDraining ? "解除手工排空" : "排空"}
                                        </Button>
                                    </Space>
                                ),
                            },
                        ]}
                    />
                    <h3>历史异常与恢复</h3>
                    {health?.history?.length ? (
                        <Table
                            size="small"
                            rowKey={(a, i) => `${a.code}-${i}`}
                            dataSource={health.history}
                            pagination={{ pageSize: 5 }}
                            scroll={{ x: 650 }}
                            columns={[
                                { title: "异常", dataIndex: "title" },
                                { title: "首次发现", dataIndex: "firstSeenAt" },
                                { title: "最近发现", dataIndex: "lastSeenAt" },
                                { title: "后端快照", render: (_, a) => a.backendSnapshotId || "未知" },
                                { title: "恢复 / 范围", render: (_, a) => a.resolvedAt || (a.historicalOnly ? "旧后端记录，当前实例未复验" : "尚未恢复") },
                            ]}
                        />
                    ) : (
                        <p>暂无可用历史事件。历史任务保留原冻结快照，不表示当前故障复发。</p>
                    )}
                    <h3>字段差异</h3>
                    <p>涉及任务时按任务冻结工具子集判断兼容性；差异左、右值与所选快照对应。后端与 worker 比较检查完整目录漂移。</p>
                    <div className="tool-snapshots-filters">
                        <Select
                            aria-label="左侧快照"
                            placeholder="左侧快照"
                            showSearch
                            optionFilterProp="label"
                            value={left || undefined}
                            onChange={(v) => query({ snapshotLeft: v })}
                            options={snapshots.map((s) => ({ value: s.id, label: `${sourceLabels[s.source] || s.source} · ${s.id}` }))}
                        />
                        <Select
                            aria-label="右侧快照"
                            placeholder="右侧快照"
                            showSearch
                            optionFilterProp="label"
                            value={right || undefined}
                            onChange={(v) => query({ snapshotRight: v })}
                            options={snapshots.map((s) => ({ value: s.id, label: `${sourceLabels[s.source] || s.source} · ${s.id}` }))}
                        />
                    </div>
                    {comparison ? (
                        <>
                            <p>比较规则：{comparison.comparatorVersion || "服务端当前严格规则"} · 检测时间：{comparison.checkedAt || "服务端未提供"}</p>
                            <Alert type={comparison.compatible && !comparison.differences.length ? "success" : "warning"} title={comparison.compatible && !comparison.differences.length ? "当前比较规则下兼容" : "存在严格校验差异；请核对字段与执行影响"} />
                            <DifferenceTable differences={comparison.differences} />
                        </>
                    ) : (
                        <p>选择两份快照后读取差异；尚未检测不代表一致。</p>
                    )}
                    <h3>快照完整工具定义</h3>
                    {detail ? (
                        <>
                            <p>
                                {detail.id} · {detail.schemaVersion} · 当前 worker 引用 {workers.filter((w) => w.snapshotId === detail.id).length} · 受影响任务{" "}
                                {detail.references?.affectedRuns ?? workers.filter((w) => w.snapshotId === detail.id).reduce((sum, w) => sum + (w.affectedRuns || 0), 0)}
                            </p>
                            <SnapshotReferences references={detail.references} />
                            <Input aria-label="查找当前快照工具" value={toolSearch} placeholder="查找工具" onChange={(e) => setToolSearch(e.target.value)} />
                            <Select
                                aria-label="查看工具定义"
                                showSearch
                                optionFilterProp="label"
                                value={toolName || undefined}
                                onChange={setToolName}
                                options={tools.filter((t) => t.function.name.toLowerCase().includes(toolSearch.toLowerCase())).map((t) => ({ value: t.function.name, label: t.function.name }))}
                            />
                            <p>每次展示一项定义；超长定义可通过诊断文件查看全文。</p>
                            {selectedTool && <SnapshotJson value={selectedTool} />}
                        </>
                    ) : (
                        <p>从列表选择快照查看完整定义。</p>
                    )}
                </div>
            </AppDrawer>
            <AppModal
                title={target?.manualDraining ? "解除手工排空" : "排空 worker"}
                open={!!target}
                confirmLoading={saving}
                okButtonProps={{ disabled: !reason.trim() }}
                onOk={() => void drain()}
                onCancel={() => {
                    if (!saving) setTarget(null);
                }}
            >
                <p>
                    {target?.instanceId} · 版本 {target?.revision}
                </p>
                <p>排空停止领取新任务，现有任务继续。解除仅移除手工排空，自动兼容性限制仍生效。</p>
                <Input.TextArea aria-label="操作原因" value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} placeholder="填写原因（必填，最多 500 字，写入审计）" />
                {operation?.failed && <Alert type="error" title={operation.text} />}
            </AppModal>
        </section>
    );
}

export function DifferenceTable({ differences }: { differences: ToolDifference[] }) {
    return (
        <Table<ToolDifference>
            size="small"
            rowKey={(d, i) => `${d.toolName}-${d.path}-${i}`}
            dataSource={differences ?? []}
            scroll={{ x: 650 }}
            pagination={{ pageSize: 10 }}
            columns={[
                {
                    title: "工具 / 字段",
                    render: (_, d) => (
                        <>
                            <div>{d.toolName}</div>
                            <code>{d.path}</code>
                        </>
                    ),
                },
                { title: "变化", dataIndex: "kind" },
                { title: "左侧", render: (_, d) => <SnapshotJson value={d.before} limit={12000} /> },
                { title: "右侧", render: (_, d) => <SnapshotJson value={d.after} limit={12000} /> },
            ]}
        />
    );
}
