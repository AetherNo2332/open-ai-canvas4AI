import { expect, test } from "bun:test";
import { reduceCrewRun, crewAvatars } from "../src/lib/canvas/crew-run-state";
import { createCrewStreamParser, type CrewRunView } from "../src/services/api/agent-crew";

const run: CrewRunView = { id:"crew", crewId:"config", canvasId:"canvas", coordinatorRunId:"coord", status:"running", revision:1, workspaceHash:"hash", workspaceRevision:1, latestSequence:0, createdAt:"now", members:[{id:"member-run", memberId:"member", agentRunId:"agent", role:"member", name:"编剧", permissionMode:"propose", status:"queued", attempt:1}], budget:{maxCredits:10,maxSteps:20,maxGenerationTasks:0,maxVideoSeconds:0,maxConcurrentMembers:2} };
test("business reducer ignores duplicate and foreign events and maps durable member state", () => {
  const event = {type:"member_run_started", crewRunId:"crew", memberRunId:"member-run", sequence:1, payload:{status:"running"}};
  const next = reduceCrewRun(run, event);
  expect(crewAvatars(next)[0].state).toBe("running");
  expect(reduceCrewRun(next, event)).toBe(next);
  expect(reduceCrewRun(next, {...event, crewRunId:"other",sequence:2})).toBe(next);
  const done = reduceCrewRun(next, {...event,type:"member_run_completed",sequence:2,payload:{status:"completed",summary:"摘要"}});
  expect(crewAvatars(done)[0].state).toBe("done");
});
test("snapshot and heartbeat never advance the business cursor", () => {
  const received: unknown[] = []; let cursor = 0;
  const parser = createCrewStreamParser(value => received.push(value), sequence => {cursor=sequence;});
  parser.push(`event: crew_snapshot\ndata: ${JSON.stringify({...run,latestSequence:9})}\n\n: heartbeat\n\n`);
  expect(cursor).toBe(0);
  parser.push(`id: 2\nevent: crew_event\ndata: ${JSON.stringify({type:"member_run_started",crewRunId:"crew",sequence:2,payload:{status:"running"}})}\n\n`);
  expect(cursor).toBe(2);
  expect(received.length).toBe(2);
  expect(() => parser.push('event: error\ndata: {"message":"failed"}\n\n')).toThrow();
});

test("foreign stream events cannot move the selected Crew cursor", () => {
  let cursor=0; const received:unknown[]=[];
  const parser=createCrewStreamParser(item=>received.push(item),sequence=>{cursor=sequence;},"crew");
  parser.push(`id: 99\nevent: crew_event\ndata: ${JSON.stringify({type:"member_run_started",crewRunId:"other",sequence:99,payload:{status:"running"}})}\n\n`);
  expect(cursor).toBe(0); expect(received).toHaveLength(0);
});
