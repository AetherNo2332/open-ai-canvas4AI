import { Button, Input, Select, Switch } from "antd";
import { useEffect, useMemo, useRef, useState } from "react";
import type { CrewMemberInput, CrewView } from "@/services/api/agent-crew";
import { ModelPicker } from "@/components/model-picker";
import { useEffectiveConfig, selectableModelsByCapability } from "@/stores/use-config-store";
import { crewModelSelection, mergeCrewDraft } from "@/lib/canvas/crew-edit-recovery";

export function CrewSettings({ crew, onSave, onDelete, disabled=false }: { crew?: CrewView; disabled?: boolean; onSave: (value: {name:string;description:string;status:string;members:CrewMemberInput[]}) => Promise<void>; onDelete?: () => Promise<void> }) {
  const [name,setName]=useState(crew?.name ?? ""); const [description,setDescription]=useState(crew?.description ?? ""); const [enabled,setEnabled]=useState(crew?.status === "enabled"); const [busy,setBusy]=useState(false); const [error,setError]=useState("");
  const base=useRef(crew);
  useEffect(()=>{const previous=base.current;if(crew&&previous){setName(draft=>mergeCrewDraft(previous.name,draft,crew.name));setDescription(draft=>mergeCrewDraft(previous.description,draft,crew.description));setEnabled(draft=>mergeCrewDraft(previous.status==="enabled",draft,crew.status==="enabled"));}base.current=crew;},[crew]);
  const members=crew?.members ?? [];
  const locked=busy||disabled;
  const action=async(fn:()=>Promise<void>)=>{if(locked)return;setBusy(true);setError("");try{await fn();}catch(cause){setError(cause instanceof Error?cause.message:String(cause));}finally{setBusy(false);}};
  const save=()=>action(()=>onSave({name,description,status:enabled?"enabled":"disabled",members:members.map(({id,skills,...member})=>member)}));
  return <section aria-label="Crew 设置" className="agent-crew-settings space-y-3"><header><h3>Crew 子代理</h3><p>配置只影响新 Run；成员使用独立会话和冻结技能快照。</p></header><label>名称<Input aria-label="Crew 名称" value={name} onChange={e=>setName(e.target.value)} disabled={locked}/></label><label>说明<Input.TextArea aria-label="Crew 说明" value={description} onChange={e=>setDescription(e.target.value)} disabled={locked}/></label><label className="flex items-center gap-2">启用<Switch aria-label="启用 Crew" checked={enabled} onChange={setEnabled} disabled={locked}/></label><dl>{members.map(member=><div key={member.name}><dt>{member.name}</dt><dd>{member.role} · {member.permissionMode} · {member.budget.maxSteps} steps</dd></div>)}</dl><div className="flex gap-2"><Button type="primary" onClick={()=>void save()} disabled={locked||!name.trim()}>保存 Crew</Button>{onDelete?<Button danger onClick={()=>void action(onDelete)} disabled={locked}>删除 Crew</Button>:null}</div>{error?<p role="alert">{error}</p>:null}</section>;
}

export function CrewMemberEditor({ value, onChange, disabled=false }: { value: CrewMemberInput; disabled?:boolean; onChange: (value:CrewMemberInput)=>void }) {
  const config=useEffectiveConfig();
  const managedConfig=useMemo(()=>({...config,channels:config.channels.filter(channel=>channel.scope==="system")}),[config]);
  const selected=selectableModelsByCapability(managedConfig,"text").find(option=>{try{const input=crewModelSelection(managedConfig,option);return input.model===value.modelConfig.model&&(input.logicalModelId?input.logicalModelId===value.modelConfig.logicalModelId:input.channelId===value.modelConfig.channelId&&input.channelModelKey===value.modelConfig.channelModelKey);}catch{return false;}});
  const budgetFields = [["maxCredits", "积分预算"], ["maxSteps", "步骤预算"], ["maxGenerationTasks", "生成任务预算"], ["maxVideoSeconds", "视频秒数预算"]] as const;
  return <fieldset disabled={disabled} aria-label={`成员 ${value.name}`} className="space-y-3 border p-3" style={{borderRadius:"var(--card-radius)",borderColor:"var(--border)"}}>
    <legend>{value.name || "新成员"}</legend>
    <label>成员名称<Input aria-label="成员名称" value={value.name} onChange={e=>onChange({...value,name:e.target.value})}/></label>
    <label className="block">成员角色<Select disabled={disabled} aria-label="成员角色" value={value.role} options={[{value:"coordinator",label:"Coordinator 协调员"},{value:"member",label:"Member 成员"}]} onChange={role=>onChange({...value,role})}/></label>
    <label className="block">成员权限<Select disabled={disabled} aria-label="成员权限" value={value.permissionMode} options={[{value:"read_only",label:"只读"},{value:"propose",label:"提案"}]} onChange={permissionMode=>onChange({...value,permissionMode})}/></label>
    <label className="block">成员模型{disabled?<Input disabled value={value.modelConfig.model}/>:<ModelPicker config={managedConfig} value={selected} capability="text" fullWidth placeholder="选择受管文本模型" onChange={option=>onChange({...value,modelConfig:crewModelSelection(managedConfig,option)})}/>}</label>
    {!selected&&!disabled?<p role="status">请选择已配置的系统文本模型；保存时自动记录渠道与模型引用。</p>:null}
    <label className="flex items-center gap-2">启用成员<Switch aria-label="启用成员" checked={value.enabled} onChange={enabled=>onChange({...value,enabled})}/></label>
    {budgetFields.map(([key,label])=><label className="block" key={key}>{label}<Input aria-label={label} type="number" min={0} value={value.budget[key]} onChange={e=>onChange({...value,budget:{...value.budget,[key]:Number(e.target.value)}})}/></label>)}
    <label>焦点节点<Input aria-label="焦点节点" value={value.focusNodeIds.join(", ")} placeholder="画布节点 ID，以逗号分隔" onChange={e=>onChange({...value,focusNodeIds:e.target.value.split(/[,，]/).map(id=>id.trim()).filter(Boolean)})}/></label>
    <label>排序<Input aria-label="成员排序" type="number" min={0} value={value.position} onChange={e=>onChange({...value,position:Number(e.target.value)})}/></label>
  </fieldset>;
}
