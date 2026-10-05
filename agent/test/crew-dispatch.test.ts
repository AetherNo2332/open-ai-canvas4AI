import assert from "node:assert/strict";
import test from "node:test";
import { validateCrewEnvelope } from "../src/crew-wire.js";

const member = { crewRunId: "cr-1", memberRunId: "mr-2", memberId: "member-2", role: "member", permissionMode: "propose", attempt: 1,
  task: { taskId: "art", title: "Art", instructions: "Return summary", artifactIds: [], focusNodeIds: [] } };
test("member envelope requires a task and rejects coordinator tools and canvas writes", () => {
  validateCrewEnvelope(member, [{ name: "task_result", allowed: true }]);
  assert.throws(() => validateCrewEnvelope({ ...member, task: undefined }, []));
  assert.throws(() => validateCrewEnvelope(member, [{ name: "delegate_task", allowed: true }]));
  assert.throws(() => validateCrewEnvelope(member, [{ name: "canvas_apply_ops", allowed: true }]));
  assert.throws(() => validateCrewEnvelope({ ...member, attempt: 0 }, []));
  assert.throws(() => validateCrewEnvelope({ ...member, transcript: [] }, []));
});
test("coordinator envelope exposes authorized targets and refuses result submission", () => {
  const coordinator = { crewRunId: "cr-1", memberRunId: "mr-1", memberId: "coord", role: "coordinator", permissionMode: "propose", attempt: 1,
    members: [{ memberId: "member-2", name: "Art", permissionMode: "propose" }] };
  validateCrewEnvelope(coordinator, [{ name: "delegate_task", allowed: true }, { name: "crew_wait", allowed: true }]);
  assert.throws(() => validateCrewEnvelope(coordinator, [{ name: "task_result", allowed: true }]));
  validateCrewEnvelope(undefined, []);
});
