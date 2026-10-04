import type { CrewEvent, CrewRunView } from "@/services/api/agent-crew";
import type { AgentSubagentAvatarItem } from "@/components/canvas/canvas-agent-subagent-list";

export function reduceCrewRun(current:CrewRunView, event:CrewEvent):CrewRunView {
  if(event.crewRunId !== current.id || event.sequence <= current.latestSequence) return current;
  const next={...current,latestSequence:event.sequence};
  if(event.memberRunId) next.members=current.members.map(member=>{
    if(member.id !== event.memberRunId) return member;
    const status=event.payload.status;
    return {...member, ...(typeof status === "string" && ["queued","running","waiting","completed","failed","cancelled"].includes(status) ? {status:status as typeof member.status}:{}),
      ...(typeof event.payload.summary === "string" ? {summary:event.payload.summary}:{}), ...(typeof event.payload.errorCode === "string" ? {errorCode:event.payload.errorCode}:{}), ...(typeof event.payload.taskId === "string" ? {taskId:event.payload.taskId}:{})};
  });
  else if(typeof event.payload.status === "string" && ["queued","running","waiting_member","waiting_approval","completed","failed","cancelled"].includes(event.payload.status)) next.status=event.payload.status as typeof current.status;
  return next;
}

export function crewAvatars(run:CrewRunView|null):AgentSubagentAvatarItem[] {
  return (run?.members ?? []).filter(member=>member.role === "member").map(member=>({id:member.id,name:member.name,avatarUrl:"/logo.svg",state:member.status === "running" || member.status === "queued" ? "running":member.status === "completed" ? "done":member.status === "failed" || member.status === "cancelled" ? "failed":"waiting"}));
}
