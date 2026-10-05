import { Button, Input, Select, Switch } from "antd";
import { useState } from "react";
import type { CrewMemberInput, CrewView } from "@/services/api/agent-crew";

export function CrewSettings({ crew, onSave, onDelete, disabled=false }: { crew?: CrewView; disabled?: boolean; onSave: (value: {name:string;description:string;status:string;members:CrewMemberInput[]}) => Promise<void>; onDelete?: () => Promise<void> }) {
  const [name,setName]=useState(crew?.name ?? ""); const [description,setDescription]=useState(crew?.description ?? ""); const [enabled,setEnabled]=useState(crew?.status === "enabled"); const [busy,setBusy]=useState(false); const [error,setError]=useState("");
  const members=crew?.members ?? [];
  const locked=busy||disabled;
  const action=async(fn:()=>Promise<void>)=>{if(locked)return;setBusy(true);setError("");try{await fn();}catch(cause){setError(cause instanceof Error?cause.message:String(cause));}finally{setBusy(false);}};
  const save=()=>action(()=>onSave({name,description,status:enabled?"enabled":"disabled",members:members.map(({id,skills,...member})=>member)}));
  return <section aria-label="Crew 设置" className="agent-crew-settings space-y-3"><header><h3>Crew 子代理</h3><p>配置只影响新 Run；成员使用独立会话和冻结技能快照。</p></header><label>名称<Input aria-label="Crew 名称" value={name} onChange={e=>setName(e.target.value)} disabled={locked}/></label><label>说明<Input.TextArea aria-label="Crew 说明" value={description} onChange={e=>setDescription(e.target.value)} disabled={locked}/></label><label className="flex items-center gap-2">启用<Switch aria-label="启用 Crew" checked={enabled} onChange={setEnabled} disabled={locked}/></label><dl>{members.map(member=><div key={member.name}><dt>{member.name}</dt><dd>{member.role} · {member.permissionMode} · {member.budget.maxSteps} steps</dd></div>)}</dl><div className="flex gap-2"><Button type="primary" onClick={()=>void save()} disabled={locked||!name.trim()}>保存 Crew</Button>{onDelete?<Button danger onClick={()=>void action(onDelete)} disabled={locked}>删除 Crew</Button>:null}</div>{error?<p role="alert">{error}</p>:null}</section>;
}

export function CrewMemberEditor({ value, onChange }: { value: CrewMemberInput; onChange: (value:CrewMemberInput)=>void }) {
  const budgetFields = [["maxCredits", "积分预算"], ["maxSteps", "步骤预算"], ["maxGenerationTasks", "生成任务预算"], ["maxVideoSeconds", "视频秒数预算"]] as const;
  return <fieldset aria-label={`成员 ${value.name}`} className="space-y-3 border p-3" style={{borderRadius:"var(--card-radius)",borderColor:"var(--border)"}}>
    <legend>{value.name || "新成员"}</legend>
    <label>成员名称<Input aria-label="成员名称" value={value.name} onChange={e=>onChange({...value,name:e.target.value})}/></label>
    <label className="block">成员角色<Select aria-label="成员角色" value={value.role} options={[{value:"coordinator",label:"Coordinator 协调员"},{value:"member",label:"Member 成员"}]} onChange={role=>onChange({...value,role})}/></label>
    <label className="block">成员权限<Select aria-label="成员权限" value={value.permissionMode} options={[{value:"read_only",label:"只读"},{value:"propose",label:"提案"}]} onChange={permissionMode=>onChange({...value,permissionMode})}/></label>
    <label>成员模型<Input aria-label="成员模型" value={value.modelConfig.model} onChange={e=>onChange({...value,modelConfig:{...value.modelConfig,model:e.target.value}})}/></label>
    <label>逻辑模型 ID<Input aria-label="逻辑模型 ID" value={value.modelConfig.logicalModelId || ""} onChange={e=>onChange({...value,modelConfig:{model:value.modelConfig.model,logicalModelId:e.target.value || undefined}})}/></label>
    <label>渠道 ID<Input aria-label="渠道 ID" value={value.modelConfig.channelId || ""} onChange={e=>onChange({...value,modelConfig:{model:value.modelConfig.model,channelId:e.target.value || undefined,channelModelKey:e.target.value ? value.modelConfig.channelModelKey : undefined}})}/></label>
    {value.modelConfig.channelId ? <label>渠道模型 key<Input aria-label="渠道模型 key" value={value.modelConfig.channelModelKey || ""} onChange={e=>onChange({...value,modelConfig:{...value.modelConfig,channelModelKey:e.target.value || undefined}})}/></label>:null}
    <label className="flex items-center gap-2">启用成员<Switch aria-label="启用成员" checked={value.enabled} onChange={enabled=>onChange({...value,enabled})}/></label>
    {budgetFields.map(([key,label])=><label className="block" key={key}>{label}<Input aria-label={label} type="number" min={0} value={value.budget[key]} onChange={e=>onChange({...value,budget:{...value.budget,[key]:Number(e.target.value)}})}/></label>)}
    <label>焦点节点<Input aria-label="焦点节点" value={value.focusNodeIds.join(", ")} placeholder="画布节点 ID，以逗号分隔" onChange={e=>onChange({...value,focusNodeIds:e.target.value.split(/[,，]/).map(id=>id.trim()).filter(Boolean)})}/></label>
    <label>排序<Input aria-label="成员排序" type="number" min={0} value={value.position} onChange={e=>onChange({...value,position:Number(e.target.value)})}/></label>
  </fieldset>;
}
