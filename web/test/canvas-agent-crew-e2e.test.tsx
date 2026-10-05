import { expect, test } from "bun:test";
import { apiClient } from "../src/services/api/request";
import { createCrewRun, createCrewStreamParser, decideCrewApproval, commitCrewProposal, cancelCrewRun, type CrewRunView } from "../src/services/api/agent-crew";
import { reduceCrewRun } from "../src/lib/canvas/crew-run-state";

// Cross-layer transport/state contract. Real DOM/model acceptance is recorded separately.
test("Crew run uses its own endpoints through completion, approval and commit", async () => {
  const original = apiClient.defaults.adapter;
  const requests: Array<{url:string;body:Record<string,unknown>}> = [];
  const snapshot: CrewRunView = {id:"run",crewId:"crew",canvasId:"canvas",coordinatorRunId:"coordinator",status:"running",revision:1,workspaceHash:"hash",workspaceRevision:1,latestSequence:0,createdAt:"now",members:[],budget:{maxCredits:10,maxSteps:30,maxGenerationTasks:0,maxVideoSeconds:0,maxConcurrentMembers:2}};
  apiClient.defaults.adapter = async config => {
    const body = JSON.parse(config.data);
    requests.push({url:config.url!,body});
    return {status:200,statusText:"OK",headers:{},config,data:{code:0,data:config.url!.includes("approvals")?{...snapshot,status:"waiting_approval",approval:{approvalId:"approval",snapshotHash:"frozen",preview:{},decision:body.decision}}:snapshot,msg:"ok"}};
  };
  try {
    let current = await createCrewRun("crew",{prompt:"任务",idempotencyKey:"request-key"});
    const parser = createCrewStreamParser(item => {current="snapshot" in item?item.snapshot:reduceCrewRun(current,item);},()=>{},"run");
    parser.push(`id: 1\nevent: crew_event\ndata: ${JSON.stringify({type:"crew_waiting_approval",crewRunId:"run",sequence:1,payload:{status:"waiting_approval"}})}\n\n`);
    expect(current.status).toBe("waiting_approval");
    current=await decideCrewApproval(current.id,"approval","approve");
    await commitCrewProposal(current.id,{approvalId:current.approval!.approvalId,expectedSnapshotHash:current.approval!.snapshotHash,idempotencyKey:"commit-key"});
    expect(requests).toEqual([
      {url:"/agent/crews/crew/runs",body:{prompt:"任务",idempotencyKey:"request-key"}},
      {url:"/agent/crew-runs/run/approvals/approval",body:{decision:"approve",reason:""}},
      {url:"/agent/crew-runs/run/commit",body:{approvalId:"approval",expectedSnapshotHash:"frozen",idempotencyKey:"commit-key"}},
    ]);
    await decideCrewApproval("run","other-approval","reject");
    await cancelCrewRun("run");
    expect(requests.slice(3).map(row=>row.url)).toEqual(["/agent/crew-runs/run/approvals/other-approval","/agent/crew-runs/run/cancel"]);
  } finally { apiClient.defaults.adapter=original; }
});
