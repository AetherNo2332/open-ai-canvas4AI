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
