import { useEffect, useRef, useState } from "react";
import { Button, Input, Select } from "antd";
import type { CanvasTheme } from "@/lib/canvas-theme";
import { getActiveUserScope } from "@/lib/user-scope";
import { localForageStorageForScope } from "@/lib/localforage-storage";
import { createUuid } from "@/lib/client-id";
import { saveRemoteUserDataNow, refreshCanvasAfterAgent } from "@/services/user-data-sync";
import { createCrewRun, listCrews, listCrewRuns, getCrewRun, subscribeCrewEvents, cancelCrewRun, decideCrewApproval, commitCrewProposal, type CrewRunView, type CrewView } from "@/services/api/agent-crew";
import { reduceCrewRun } from "@/lib/canvas/crew-run-state";
import { CrewRunCard } from "./canvas-agent-crew-run-card";
import { canReleaseCrewPending, crewCommitToRefresh, createCrewMutationGate } from "@/lib/canvas/crew-edit-recovery";

type PendingCrew = {crewId:string;prompt:string;skillIds:string[];idempotencyKey:string};
export function CanvasAgentCrewConsole({canvasId,theme,skillIds,onConfigure}:{canvasId:string;theme:CanvasTheme;skillIds:string[];onConfigure:()=>void}){
  const [crews,setCrews]=useState<CrewView[]>([]);
  const [selected,setSelected]=useState("");
  const [runs,setRuns]=useState<CrewRunView[]>([]);
  const [run,setRun]=useState<CrewRunView|null>(null);
  const [prompt,setPrompt]=useState("");
  const [busy,setBusy]=useState(false);
  const [ready,setReady]=useState(false);
  const [error,setError]=useState("");
  const [connection,setConnection]=useState("connecting");
  const [epoch,setEpoch]=useState(0);
  const pending=useRef<PendingCrew|null>(null);
  const refreshedCommit=useRef<string|null>(null);
  const [refreshError,setRefreshError]=useState("");
  const [refreshEpoch,setRefreshEpoch]=useState(0);
  const mutationGate=useRef(createCrewMutationGate()).current;
  const mounted=useRef(true);
  const storage=useRef(localForageStorageForScope(getActiveUserScope())).current;
  const pendingKey=`crew-pending:${canvasId}`;
  useEffect(()=>{mounted.current=true;const controller=new AbortController();void (async()=>{
    try{
      const [configs,history,raw]=await Promise.all([listCrews(canvasId,controller.signal),listCrewRuns(canvasId,controller.signal),storage.getItem(pendingKey)]);
      if(controller.signal.aborted)return;
      const saved=raw?JSON.parse(raw) as PendingCrew:null;
      if(saved&&(!saved.crewId||!saved.idempotencyKey||typeof saved.prompt!=="string"||!Array.isArray(saved.skillIds)))throw new Error("Crew 待确认提交记录损坏");
      pending.current=saved;setCrews(configs);setSelected(saved?.crewId||configs.find(crew=>crew.status==="enabled")?.id||"");setRuns(history);setRun(history[0]??null);if(saved)setPrompt(saved.prompt);setReady(true);
    }catch(cause){if(!controller.signal.aborted)setError(cause instanceof Error?cause.message:String(cause));}
  })();return()=>{mounted.current=false;controller.abort();};},[canvasId]);
  useEffect(()=>{
    if(!run)return;
    return subscribeCrewEvents(run.id,item=>{if(!mounted.current)return;setRun(current=>{
      if(!current||current.id!==run.id)return current;
      if("snapshot" in item)return item.snapshot.canvasId===canvasId&&item.snapshot.revision>=current.revision?item.snapshot:current;
      return reduceCrewRun(current,item);
    });},{after:run.latestSequence,onConnectionChange:status=>{if(mounted.current)setConnection(status);},onError:cause=>{if(mounted.current)setError(cause instanceof Error?cause.message:String(cause));}});
  },[run?.id,canvasId,epoch]);
  useEffect(()=>{
    const operation=crewCommitToRefresh(run,canvasId,refreshedCommit.current);
    if(!operation||!run)return;
    let cancelled=false;const committedRunId=run.id;
    void refreshCanvasAfterAgent(canvasId).then(async()=>{
      if(cancelled||!mounted.current)return;
      refreshedCommit.current=operation;setRefreshError("");
      await storage.removeItem(`crew-commit:${canvasId}:${committedRunId}`);
    }).catch(cause=>{if(!cancelled&&mounted.current)setRefreshError(cause instanceof Error?cause.message:String(cause));});
    return()=>{cancelled=true;};
  },[run?.approval?.operationId,canvasId,refreshEpoch]);
  const active=Boolean(run&&!['completed','failed','cancelled'].includes(run.status));
  const action=(fn:()=>Promise<void>)=>mutationGate.run(async()=>{setBusy(true);setError("");try{await fn();}catch(cause){if(mounted.current)setError(cause instanceof Error?cause.message:String(cause));}finally{if(mounted.current)setBusy(false);}}).catch(()=>undefined);
  const submit=()=>action(async()=>{
    if(!ready||!selected||!prompt.trim()||(active&&!pending.current))return;
    if(pending.current&&(pending.current.crewId!==selected||pending.current.prompt!==prompt.trim()))throw new Error("上一条 Crew 请求结果待确认，请原样重试；不能覆盖原幂等记录。");
    await saveRemoteUserDataNow();
    if(!mounted.current)return;
    const hadAmbiguousRequest=Boolean(pending.current);
    const request=pending.current||{crewId:selected,prompt:prompt.trim(),skillIds:[...skillIds],idempotencyKey:createUuid()};
    await storage.setItem(pendingKey,JSON.stringify(request));pending.current=request;
    try{
      const result=await createCrewRun(request.crewId,{prompt:request.prompt,skillIds:request.skillIds,idempotencyKey:request.idempotencyKey});
      if(!mounted.current)return;
      setRun(result);setRuns(rows=>[result,...rows.filter(row=>row.id!==result.id)]);
      await storage.removeItem(pendingKey);pending.current=null;
      setPrompt("");
    }catch(cause){
      if(canReleaseCrewPending(hadAmbiguousRequest,(cause as {status?:number}).status||0)){await storage.removeItem(pendingKey);pending.current=null;}
      throw cause;
    }
  });
  const approve=(decision:"approve"|"reject")=>action(async()=>{if(!run?.approval)return;const next=await decideCrewApproval(run.id,run.approval.approvalId,decision);if(mounted.current)setRun(next);});
  const commit=()=>action(async()=>{
    if(!run?.approval||run.approval.decision!=="approve")return;
    const key=`crew-commit:${canvasId}:${run.id}`;
    const raw=await storage.getItem(key);
    const request=raw?JSON.parse(raw) as {approvalId:string;expectedSnapshotHash:string;idempotencyKey:string}:{approvalId:run.approval.approvalId,expectedSnapshotHash:run.approval.snapshotHash,idempotencyKey:createUuid()};
    if(request.approvalId!==run.approval.approvalId||request.expectedSnapshotHash!==run.approval.snapshotHash||!request.idempotencyKey)throw new Error("Crew 待确认提交与当前审批不一致，请重新读取运行");
    await storage.setItem(key,JSON.stringify(request));
    if(!mounted.current)return;
    await commitCrewProposal(run.id,request);
    const next=await getCrewRun(run.id);if(mounted.current)setRun(next);
  });
  return <section aria-label="Crew 工作台" className="flex min-h-0 flex-1 flex-col gap-3 p-4">
    <div className="flex gap-2"><Select aria-label="选择运行 Crew" className="min-w-0 flex-1" value={selected||undefined} disabled={busy||active||Boolean(pending.current)} options={crews.filter(crew=>crew.status==="enabled").map(crew=>({value:crew.id,label:crew.name}))} onChange={setSelected}/><Button onClick={onConfigure}>配置 Crew</Button></div>
    {!selected&&ready?<p>当前画布没有启用的 Crew，请先配置 Coordinator 与成员。</p>:null}
    {runs.length?<Select aria-label="Crew 运行历史" value={run?.id} disabled={busy||active} options={runs.map(row=>({value:row.id,label:`${row.createdAt} · ${row.status}`}))} onChange={id=>{void action(async()=>{const next=await getCrewRun(id);if(mounted.current)setRun(next);});}}/>:null}
    <div className="min-h-0 flex-1 overflow-y-auto">{run?<CrewRunCard run={run} theme={theme} busy={busy} onCancel={()=>void action(async()=>{const next=await cancelCrewRun(run.id);if(mounted.current)setRun(next);})} onApprove={()=>void approve("approve")} onReject={()=>void approve("reject")} onCommit={()=>void commit()}/>:null}</div>
    {run?<p role="status">事件连接：{connection}{connection==="disconnected"&&active?<Button size="small" onClick={()=>setEpoch(value=>value+1)}>重新连接</Button>:null}</p>:null}
    {error?<p role="alert">{error}</p>:null}
    {refreshError?<div role="alert"><p>{refreshError}</p><Button onClick={()=>setRefreshEpoch(value=>value+1)}>重新加载已提交画布</Button></div>:null}
    {pending.current?<p role="status">待确认请求已保留，请原样重试。</p>:null}
    <Input.TextArea aria-label="Crew 任务" value={prompt} onChange={e=>setPrompt(e.target.value)} disabled={busy||(active&&!pending.current)||!ready} placeholder="输入任务，由 Coordinator 委派给成员" autoSize={{minRows:2,maxRows:6}}/>
    <Button type="primary" onClick={()=>void submit()} disabled={busy||(active&&!pending.current)||!ready||!selected||!prompt.trim()} loading={busy}>{pending.current?"确认上次 Crew 提交":"启动 Crew"}</Button>
  </section>;
}
