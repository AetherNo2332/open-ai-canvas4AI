import { useEffect, useRef, useState } from "react";
import { Button, Checkbox, Select } from "antd";
import { useUserStore } from "@/stores/use-user-store";
import type { Skill } from "@/services/api/skills";
import { listCrews, createCrew, updateCrew, deleteCrew, addCrewMember, updateCrewMember, deleteCrewMember, replaceCrewMemberSkills, type CrewView, type CrewMemberView, type CrewMemberInput } from "@/services/api/agent-crew";
import { CrewSettings, CrewMemberEditor } from "./canvas-agent-crew-settings";
import { createCrewMutationGate, mergeCrewDraft } from "@/lib/canvas/crew-edit-recovery";

export function CanvasAgentCrewSettings({ canvasId, availableSkills, onChanged }: {canvasId:string;availableSkills:Skill[];onChanged?:()=>void}) {
  const userId = useUserStore(state=>state.user?.id);
  const enabled = useUserStore(state=>state.features.agentCrewEnabled);
  const [crews,setCrews] = useState<CrewView[]>([]);
  const [selected,setSelected] = useState("");
  const [error,setError] = useState("");
  const [busy,setBusy] = useState(false);
  const mutationGate=useRef(createCrewMutationGate()).current;
  const scope = `${userId}:${canvasId}`;
  useEffect(()=>{const controller=new AbortController();setCrews([]);setSelected("");setError("");if(enabled&&userId) void listCrews(canvasId,controller.signal).then(rows=>{if(!controller.signal.aborted){setCrews(rows);setSelected(rows[0]?.id || "");}}).catch(cause=>{if(!controller.signal.aborted)setError(String(cause));});return()=>controller.abort();},[scope,enabled]);
  const crew=crews.find(row=>row.id===selected);
  const replace=(view:CrewView)=>{setCrews(rows=>rows.some(row=>row.id===view.id)?rows.map(row=>row.id===view.id?view:row):[...rows,view]);setSelected(view.id);onChanged?.();};
  const mutate=(action:()=>Promise<void>)=>mutationGate.run(async()=>{setBusy(true);setError("");try{await action();}catch(cause){if((cause as {reason?:string}).reason==="agent_crew_revision_conflict"){setError("Crew 已被其他页面修改。未编辑字段已读取最新版本；本地草稿保留，请核对后再次保存。");const rows=await listCrews(canvasId);setCrews(rows);}else setError(cause instanceof Error?cause.message:String(cause));throw cause;}finally{setBusy(false);}});
  if(!enabled)return <p role="status">Crew 功能未启用</p>;
  return <div key={scope} className="canvas-agent-settings-scroll thin-scrollbar min-h-0 flex-1 overflow-y-auto p-4 space-y-4">
    <p>各画布的 Crew 配置独立管理。配置只影响新 Run；已创建的运行保留冻结快照。</p>
    <div className="flex gap-2"><Select disabled={busy} aria-label="选择 Crew 配置" value={selected||undefined} placeholder="当前画布暂无 Crew" options={crews.map(row=>({value:row.id,label:row.name}))} onChange={setSelected} className="min-w-0 flex-1"/><Button onClick={()=>setSelected("")} disabled={busy}>新建 Crew</Button></div>
    <CrewSettings key={crew?.id || "new"} crew={crew} disabled={busy} onSave={async input=>{await mutate(async()=>{replace(crew?await updateCrew(crew.id,crew.revision,input):await createCrew(canvasId,input));});}} onDelete={crew?async()=>{await mutate(async()=>{await deleteCrew(crew.id,crew.revision);setCrews(rows=>rows.filter(row=>row.id!==crew.id));setSelected("");onChanged?.();});}:undefined}/>
    {crew ? <fieldset disabled={busy} className="space-y-4"><h4>成员与技能 · 修订 {crew.revision}</h4>
      <label className="block">Coordinator<Select disabled={busy} aria-label="选择 Coordinator" value={crew.members.find(member=>member.role==="coordinator")?.id} options={crew.members.filter(member=>member.enabled).map(member=>({value:member.id,label:member.name}))} onChange={id=>{void mutate(async()=>{replace(await updateCrew(crew.id,crew.revision,{name:crew.name,description:crew.description,status:crew.status,coordinatorMemberId:id}));}).catch(()=>undefined);}}/></label>
      {crew.members.map(member=><MemberForm key={member.id} busy={busy} member={member} availableSkills={availableSkills} onSave={value=>mutate(async()=>{replace(await updateCrewMember(member.id,crew.revision,value));})} onDelete={()=>mutate(async()=>{replace(await deleteCrewMember(member.id,crew.revision));})} onSaveSkills={skills=>mutate(async()=>{replace(await replaceCrewMemberSkills(member.id,crew.revision,skills));})}/>)}
      <NewMemberForm key={crew.id+":"+crew.members.length} busy={busy} coordinator={!crew.members.some(member=>member.role==="coordinator")} position={crew.members.length} onSave={value=>mutate(async()=>{replace(await addCrewMember(crew.id,crew.revision,value));})}/>
    </fieldset>:<p>先创建 Crew，再添加成员和技能。启用前需配置一个 Coordinator。</p>}
    {error?<p role="alert">{error}</p>:null}
  </div>;
}
function validModel(value:CrewMemberInput){return Boolean(value.modelConfig.model.trim()&&(value.modelConfig.logicalModelId||value.modelConfig.channelId&&value.modelConfig.channelModelKey));}
function NewMemberForm({coordinator,position,onSave,busy}:{coordinator:boolean;position:number;busy:boolean;onSave:(value:CrewMemberInput)=>Promise<void>}){
  const [value,setValue]=useState<CrewMemberInput>({name:coordinator?"Coordinator":"新成员",role:coordinator?"coordinator":"member",permissionMode:"propose",enabled:true,position,focusNodeIds:[],modelConfig:{model:""},budget:{maxCredits:100,maxSteps:30,maxGenerationTasks:0,maxVideoSeconds:0}});
  return <section><CrewMemberEditor disabled={busy} value={value} onChange={setValue}/><Button onClick={()=>void onSave(value).catch(()=>undefined)} disabled={busy||!value.name.trim()||!validModel(value)}>添加成员</Button></section>;
}
function MemberForm({member,availableSkills,onSave,onDelete,onSaveSkills,busy}:{member:CrewMemberView;availableSkills:Skill[];busy:boolean;onSave:(value:CrewMemberInput)=>Promise<void>;onDelete:()=>Promise<void>;onSaveSkills:(skills:CrewMemberView["skills"])=>Promise<void>}){
  const [value,setValue]=useState<CrewMemberInput>(member);
  const [skills,setSkills]=useState(member.skills);
  const base=useRef(member);
  useEffect(()=>{const previous=base.current;setValue(draft=>mergeCrewDraft(previous,draft,member));setSkills(draft=>mergeCrewDraft(previous.skills,draft,member.skills));base.current=member;},[member]);
  return <section className="space-y-2"><CrewMemberEditor disabled={busy} value={value} onChange={setValue}/><div className="flex gap-2"><Button disabled={busy||!validModel(value)} onClick={()=>void onSave(value).catch(()=>undefined)}>保存成员</Button><Button disabled={busy} danger onClick={()=>void onDelete().catch(()=>undefined)}>删除成员</Button></div>
    <CrewMemberSkills name={member.name} availableSkills={availableSkills} skills={skills} onChange={setSkills} onSave={onSaveSkills}/>
  </section>;
}
export function CrewMemberSkills({name,availableSkills,skills,onChange,onSave}:{name:string;availableSkills:Skill[];skills:CrewMemberView["skills"];onChange:(skills:CrewMemberView["skills"])=>void;onSave:(skills:CrewMemberView["skills"])=>Promise<void>}){
  availableSkills=availableSkills.filter(skill=>!skill.isPrivate);
  const toggle=(skill:Skill,checked:boolean)=>onChange(checked?[...skills.filter(row=>row.skillId!==skill.skillId),{skillId:skill.skillId,skillVersionId:skill.versionId,contentHash:skill.contentHash,skillName:skill.skillName,enabled:true,position:skills.length}]:skills.filter(row=>row.skillId!==skill.skillId));
  return <fieldset><legend>{name} · 成员追加技能</legend>{availableSkills.map(skill=><label className="block" key={skill.skillId}><Checkbox checked={skills.some(row=>row.skillId===skill.skillId)} onChange={e=>toggle(skill,e.target.checked)}>{skill.skillName}</Checkbox></label>)}{skills.filter(row=>!availableSkills.some(skill=>skill.skillId===row.skillId)).map(row=><label className="block" key={row.skillId}><Checkbox checked onChange={()=>onChange(skills.filter(item=>item.skillId!==row.skillId))}>{row.skillName} · 冻结版本</Checkbox></label>)}<Button onClick={()=>void onSave(skills.map((row,position)=>({...row,position}))).catch(()=>undefined)}>保存成员技能</Button></fieldset>;
}
