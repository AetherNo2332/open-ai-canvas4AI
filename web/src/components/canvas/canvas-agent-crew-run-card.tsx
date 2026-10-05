import { Button, Tag } from "antd";
import type { CrewRunView } from "@/services/api/agent-crew";
import { crewAvatars } from "@/lib/canvas/crew-run-state";
import { AgentSubagentList } from "./canvas-agent-subagent-list";
import type { CanvasTheme } from "@/lib/canvas-theme";

export function CrewRunCard({ run, theme, onCancel, onApprove, onCommit }: { run:CrewRunView; theme:CanvasTheme; onCancel?:()=>void; onApprove?:()=>void; onCommit?:()=>void }) { const approval=run.approval; return <article aria-label={`Crew Run ${run.id}`} className="agent-crew-run-card space-y-2"><header className="flex items-center justify-between"><strong>Crew Run</strong><Tag color={run.status === "failed" ? "red" : run.status === "completed" ? "green" : "blue"}>{run.status}</Tag></header><AgentSubagentList items={crewAvatars(run)} theme={theme}/>{approval?<section aria-label="Crew 审批预览"><h4>统一审批预览</h4><p>{approval.summary||"成员结果已聚合"}</p>{approval.decision? <Tag>{approval.decision}</Tag>:<Button onClick={onApprove}>批准提案</Button>}{approval.decision === "approve"?<Button onClick={onCommit}>提交画布</Button>:null}</section>:null}{onCancel&&!["completed","failed","cancelled"].includes(run.status)?<Button onClick={onCancel}>取消 Crew</Button>:null}</article>; }
