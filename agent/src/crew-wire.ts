export const CREW_EVENT_TYPES = [
  "crew_run_created",
  "member_run_started",
  "member_message",
  "member_run_waiting",
  "member_run_completed",
  "member_run_failed",
  "crew_approval_required",
  "crew_run_completed",
] as const;

export type CrewEventType = typeof CREW_EVENT_TYPES[number];
export type CrewMemberRole = "coordinator" | "member";
export type CrewMemberPermission = "read_only" | "propose" | "write";
export type CrewRunStatus = "queued" | "running" | "waiting_member" | "waiting_approval" | "completed" | "failed" | "cancelled";
export type MemberRunStatus = "queued" | "running" | "waiting" | "completed" | "failed" | "cancelled";

export type CrewEvent = {
  type: CrewEventType;
  crewRunId: string;
  memberRunId?: string;
  sequence: number;
  payload: Record<string, unknown>;
};

export interface CrewEnvelope {
  crewRunId: string; memberRunId: string; memberId: string;
  role: "coordinator" | "member"; permissionMode: "read_only" | "propose"; attempt: number;
  budget?: Record<string, number>;
  resultsCollected?: boolean;
  approvalRequired?: boolean;
  task?: { taskId: string; title: string; instructions: string; artifactIds: string[]; focusNodeIds: string[] };
  members?: { memberId: string; name: string; permissionMode: "read_only" | "propose" }[];
}

// Validate before restoring entries or executing tools. Go remains the authority.
export function validateCrewEnvelope(value: unknown, tools: { name: string; allowed: boolean }[]): asserts value is CrewEnvelope | undefined {
  if (value === undefined) return;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid Crew envelope");
  const envelope = value as Record<string, unknown>;
  const allowedKeys = new Set(["crewRunId", "memberRunId", "memberId", "role", "permissionMode", "attempt", "budget", "task", "members", "resultsCollected", "approvalRequired"]);
  if (Object.keys(envelope).some((key) => !allowedKeys.has(key)) ||
    ["crewRunId", "memberRunId", "memberId"].some((key) => typeof envelope[key] !== "string" || !envelope[key]) ||
    !Number.isInteger(envelope.attempt) || Number(envelope.attempt) < 1 ||
    !["read_only", "propose"].includes(String(envelope.permissionMode)) || !["coordinator", "member"].includes(String(envelope.role))) throw new Error("Invalid Crew identity");
  const writes = new Set(["canvas_apply_ops", "canvas_arrange_nodes", "canvas_create_storyboard", "canvas_edit_storyboard", "canvas_edit_batch_table", "generate_media", "image_layer_split"]);
  for (const tool of tools) {
    if (!tool.allowed) continue;
    if (writes.has(tool.name) || (envelope.role === "member" && ["delegate_task", "crew_wait", "crew_propose", "finish_run", "ask_user"].includes(tool.name)) ||
      (envelope.role === "coordinator" && tool.name === "task_result")) throw new Error("Crew tool permission mismatch");
  }
  if (envelope.role === "member") {
    const task = envelope.task as Record<string, unknown> | undefined;
    if (!task || ["taskId", "title", "instructions"].some((key) => typeof task[key] !== "string" || !task[key]) || envelope.members !== undefined) throw new Error("Member task missing");
    if (Object.keys(task).some((key) => !["taskId", "title", "instructions", "artifactIds", "focusNodeIds"].includes(key))) throw new Error("Invalid member task fields");
  } else {
    if (envelope.task !== undefined || !Array.isArray(envelope.members) || envelope.members.some((item) => !item || typeof item.memberId !== "string" || typeof item.name !== "string" || !["read_only", "propose"].includes(item.permissionMode))) throw new Error("Coordinator targets missing");
  }
}
