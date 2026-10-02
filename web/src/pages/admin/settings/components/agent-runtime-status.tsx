import { Button, Tag, Tooltip } from "antd";
import { RefreshCw } from "lucide-react";
import { useState } from "react";
import type { AgentRuntimeStatus } from "@/services/api/admin-agent-settings";

export function AgentRuntimeStatusView({ instances, revision }: { instances: AgentRuntimeStatus[]; revision?: number }) {
    const [showOffline, setShowOffline] = useState(false);
    const online = instances.filter(row => row.online);
    const offline = instances.length - online.length;
    const visible = (showOffline ? instances : online).slice().sort((a, b) =>
        Number(b.online) - Number(a.online) || b.lastHeartbeatAt.localeCompare(a.lastHeartbeatAt));
    const total = (key: "resident" | "capacity" | "claimReservations" | "dispatchActive" | "readyQueued") => online.reduce((sum, row) => sum + row[key], 0);
    const metrics = [
        { label: "在线执行器", value: online.length, detail: `${offline} 个离线记录` },
        { label: "驻留会话", value: `${total("resident")} / ${total("capacity")}`, detail: "当前持有会话 / 在线容量" },
        { label: "执行中步骤", value: total("dispatchActive"), detail: `${total("claimReservations")} 个领取预留` },
        { label: "排队步骤", value: total("readyQueued"), detail: "仅统计在线执行器" },
    ];
    return <div className="agent-runtime">
        <div className="agent-runtime-metrics">{metrics.map(metric => <div className="agent-runtime-metric" key={metric.label}>
            <span>{metric.label}</span><strong>{metric.value}</strong><small>{metric.detail}</small>
        </div>)}</div>
        <div className="agent-runtime-toolbar">
            <span>每 5 秒自动刷新；离线数据为最后一次心跳快照。</span>
            {offline > 0 && <Button size="small" aria-pressed={showOffline} onClick={() => setShowOffline(value => !value)}>{showOffline ? "收起离线记录" : `查看离线记录（${offline}）`}</Button>}
        </div>
        {visible.length ? <div className="agent-runtime-table-wrap"><table className="agent-runtime-table">
            <caption className="sr-only">Agent 执行器运行状态</caption>
            <thead><tr>
                <th scope="col">执行器</th><th scope="col">状态</th>
                <th scope="col"><Tooltip title="会话等待模型、工具或审批时仍驻留，但不占执行槽。">驻留 / 上限</Tooltip></th>
                <th scope="col"><Tooltip title="已预留、尚未完成会话领取的容量。">领取预留</Tooltip></th>
                <th scope="col"><Tooltip title="当前正在推进的短步骤数。">槽占用</Tooltip></th>
                <th scope="col">排队步骤</th><th scope="col">配置修订号</th><th scope="col">最后心跳</th>
            </tr></thead>
            <tbody>{visible.map(row => <tr key={row.instanceId} className={row.online ? undefined : "is-offline"}>
                <td><code>{row.instanceId}</code></td>
                <td><Tag color={!row.online ? undefined : row.draining ? "warning" : "success"}>{!row.online ? "离线" : row.draining ? "缩容排空中" : "在线"}</Tag></td>
                <td className="agent-runtime-number">{row.resident} / {row.capacity}</td>
                <td className="agent-runtime-number">{row.claimReservations}</td>
                <td className="agent-runtime-number">{row.dispatchActive}</td>
                <td className="agent-runtime-number">{row.readyQueued}</td>
                <td><span className="agent-runtime-number">{row.appliedConfigRevision}</span>{row.online && revision !== undefined && row.appliedConfigRevision < revision && <Tag color="warning" icon={<RefreshCw size={12} />}>待应用</Tag>}</td>
                <td><time dateTime={row.lastHeartbeatAt}>{Number.isNaN(Date.parse(row.lastHeartbeatAt)) ? "未知" : new Date(row.lastHeartbeatAt).toLocaleString()}</time></td>
            </tr>)}</tbody>
        </table></div> : <div className="agent-runtime-empty">{instances.length ? "暂无在线执行器，可展开离线记录查看最后心跳。" : "暂无执行器心跳记录，执行器连接后会在这里显示。"}</div>}
    </div>;
}
