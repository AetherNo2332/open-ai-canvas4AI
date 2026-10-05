import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { CrewMemberEditor } from "../src/components/canvas/canvas-agent-crew-settings";
import type { CrewMemberInput } from "../src/services/api/agent-crew";
import { apiClient } from "../src/services/api/request";
import { deleteCrew, updateCrew, updateCrewMember, replaceCrewMemberSkills } from "../src/services/api/agent-crew";
const member: CrewMemberInput = {name:"编剧",role:"member",permissionMode:"propose",enabled:true,position:1,focusNodeIds:[],modelConfig:{model:"test-model"},budget:{maxCredits:10,maxSteps:20,maxGenerationTasks:0,maxVideoSeconds:0}};
test("member editor exposes name, role, budgets and canvas focus fields", () => {
  const html = renderToStaticMarkup(<CrewMemberEditor value={member} onChange={()=>{}} />);
  for (const label of ["成员名称", "成员角色", "成员权限", "成员模型", "积分预算", "步骤预算", "焦点节点"]) expect(html).toContain(label);
});
test("Crew writes project view models onto strict backend input fields",async()=>{
  const original=apiClient.defaults.adapter;
  const bodies:Record<string,unknown>[]=[];
  apiClient.defaults.adapter=async config=>{bodies.push(JSON.parse(config.data));return {status:200,statusText:"OK",headers:{},config,data:{code:0,data:{revision:8},msg:"ok"}};};
  try{
    await updateCrew("crew",7,{name:"剧组",description:"",status:"disabled",members:[]} as Parameters<typeof updateCrew>[2]);
    await updateCrewMember("member",8,{...member,id:"member",skills:[]} as CrewMemberInput);
    await replaceCrewMemberSkills("member",9,[{skillId:"skill",skillVersionId:"v1",enabled:true,position:0,contentHash:"hash",skillName:"技能"} as Parameters<typeof replaceCrewMemberSkills>[2][number]]);
    expect(bodies[0]).not.toHaveProperty("members");
    expect(bodies[1]).not.toHaveProperty("id");
    expect(bodies[1]).not.toHaveProperty("skills");
    expect(bodies[2].skills).toEqual([{skillId:"skill",skillVersionId:"v1",enabled:true,position:0}]);
  }finally{apiClient.defaults.adapter=original;}
});
test("Crew mutation requests carry revision and DELETE body through the shared transport", async () => {
  const original=apiClient.defaults.adapter;
  const requests:Array<{method?:string;url?:string;data:unknown}>=[];
  apiClient.defaults.adapter=async config=>{requests.push({method:config.method,url:config.url,data:JSON.parse(config.data)});return {status:200,statusText:"OK",headers:{},config,data:{code:0,data:{revision:8},msg:"ok"}};};
  try {
    await deleteCrew("crew/a",7);
    await updateCrewMember("member",8,member);
    await replaceCrewMemberSkills("member",9,[]);
    expect(requests[0]).toEqual({method:"delete",url:"/agent/crews/crew%2Fa",data:{revision:7}});
    expect(requests[1].data).toMatchObject({revision:8,name:"编剧",budget:member.budget});
    expect(requests[2].data).toEqual({revision:9,skills:[]});
  } finally { apiClient.defaults.adapter=original; }
});
