import { Button, Tag } from "antd";
import type { CrewRunView } from "@/services/api/agent-crew";
import { crewAvatars } from "@/lib/canvas/crew-run-state";
import { AgentSubagentList } from "./canvas-agent-subagent-list";
import type { CanvasTheme } from "@/lib/canvas-theme";

export function CrewRunCard({ run, theme, busy=false, onCancel, onApprove, onReject, onCommit }: {
  run:CrewRunView;theme:CanvasTheme;busy?:boolean;onCancel?:()=>void;onApprove?:()=>void;onReject?:()=>void;onCommit?:()=>void;
}) {
  const approval=run.approval;
  return <article aria-label={`Crew Run ${run.id}`} className="agent-crew-run-card space-y-3">
    <header className="flex items-center justify-between"><strong>Crew Run</strong><Tag>{run.status}</Tag></header>
    <AgentSubagentList items={crewAvatars(run)} theme={theme}/>
    <ul className="space-y-2">{run.members.map(member=><li key={member.id}><strong>{member.name}</strong> · {member.status}{member.taskId?<small> · {member.taskId}</small>:null}{member.summary?<p className="whitespace-pre-wrap">{member.summary}</p>:null}{member.errorCode?<p role="alert">{member.errorCode}</p>:null}</li>)}</ul>
    {approval?<section aria-label="Crew 审批预览"><h4>统一审批预览</h4><p>{approval.summary||"成员结果已聚合"}</p>
      <details><summary>查看画布变更提案</summary><pre className="max-h-64 overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify(approval.preview,null,2)}</pre></details>
      {approval.decision?<Tag>{approval.decision}</Tag>:<div className="flex gap-2"><Button onClick={onApprove} disabled={busy||!onApprove}>批准提案</Button><Button danger onClick={onReject} disabled={busy||!onReject}>拒绝提案</Button></div>}
      {approval.decision==="approve"&&!approval.operationId?<Button type="primary" onClick={onCommit} disabled={busy||!onCommit}>提交画布</Button>:null}
      {approval.operationId?<p>已提交画布 · {approval.operationId}</p>:null}
    </section>:null}
    {onCancel&&!['completed','failed','cancelled'].includes(run.status)?<Button onClick={onCancel} disabled={busy}>取消 Crew</Button>:null}
  </article>;
}
