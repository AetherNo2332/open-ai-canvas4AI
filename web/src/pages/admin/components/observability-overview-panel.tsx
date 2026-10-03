import { Alert, Button, Card, Empty, Spin, Tag } from "antd";
import { ExternalLink } from "lucide-react";
import { useEffect, useState } from "react";
import { getAdminObservabilityOverview, normalizeGrafanaUrl, type AdminObservabilityOverview } from "@/services/api/observability";

const number = (value: number | undefined) => value == null ? "—" : new Intl.NumberFormat("zh-CN").format(value);
const percent = (value: number | undefined) => value == null ? "—" : `${value.toFixed(1)}%`;

export default function ObservabilityOverviewPanel() {
    const [data, setData] = useState<AdminObservabilityOverview | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    useEffect(() => { let active = true; void getAdminObservabilityOverview().then((value) => { if (active) setData(value); }).catch((reason) => { if (active) setError(reason instanceof Error ? reason.message : "读取运行观测失败"); }).finally(() => { if (active) setLoading(false); }); return () => { active = false; }; }, []);
    if (loading) return <div className="admin-observability-state"><Spin /> 正在读取运行观测</div>;
    if (error) return <Alert type="warning" showIcon message="运行观测不可用" description={error} />;
    if (!data || !data.available) return <Empty description={data?.delayed ? "观测数据延迟" : "暂无运行观测数据"} />;
    const cards = [
        ["Worker 在线", number(data.worker.online)], ["队列深度", number(data.queue.depth)], ["真实成功率", percent(data.tasks.successRate)], ["失败 / 重试", `${number(data.tasks.failed)} / ${number(data.tasks.retried)}`],
        ["P95 延迟", `${number(data.tasks.p95LatencyMs)} ms`], ["工具成功率", percent(data.tools.successRate)], ["LLM 调用", number(data.llm.calls)], ["输入 / 输出 Token", `${number(data.llm.inputTokens)} / ${number(data.llm.outputTokens)}`],
    ];
    const grafanaUrl = normalizeGrafanaUrl(data.grafanaUrl);
    return <div className="admin-observability-panel">
        {grafanaUrl ? <div className="admin-observability-actions"><Button type="default" icon={<ExternalLink className="size-4" />} onClick={() => window.open(grafanaUrl, "_blank", "noopener,noreferrer")}>在 Grafana 中查看</Button></div> : null}
        {data.alerts.length ? <Alert type="warning" showIcon message="当前告警" description={<div>{data.alerts.map((item) => <Tag key={item}>{item}</Tag>)}</div>} /> : null}
        <div className="admin-observability-grid">{cards.map(([label, value]) => <Card size="small" key={label}><div className="admin-observability-card-label">{label}</div><div className="admin-observability-card-value">{value}</div></Card>)}</div>
        <div className="admin-observability-failures"><h3>最近异常任务</h3>{data.recentFailures.length ? data.recentFailures.map((item) => <div className="admin-observability-failure" key={`${item.runId}-${item.at}`}><strong>{item.status}</strong><span>{item.reason || "未提供原因"}</span><code>{item.traceId}</code></div>) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无异常任务" />}</div>
    </div>;
}
