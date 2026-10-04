import assert from "node:assert/strict";
import test from "node:test";
import { CREW_EVENT_TYPES, type CrewEvent, type CrewMemberPermission, type CrewMemberRole } from "../src/crew-wire.js";

test("crew wire contract exposes stable public event names and member roles", () => {
  const role: CrewMemberRole = "coordinator";
  const permission: CrewMemberPermission = "propose";
  const event: CrewEvent = { type: "member_run_started", crewRunId: "crew-1", memberRunId: "member-1", sequence: 1, payload: { taskId: "task-1", role, permission } };
  assert.deepEqual(CREW_EVENT_TYPES, ["crew_run_created", "member_run_started", "member_message", "member_run_waiting", "member_run_completed", "member_run_failed", "crew_approval_required", "crew_run_completed"]);
  assert.equal(event.payload.permission, permission);
});
