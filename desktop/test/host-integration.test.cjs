const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { randomBytes } = require('node:crypto');
const { startHost } = require('../runtime.cjs');

test('real host persists owner and CAS canvas across restart', {skip:!process.env.CANVAS_TEST_RESOURCES}, async () => {
  const dataDir=fs.mkdtempSync(path.join(os.tmpdir(),'canvas-host-中文 '));
  const masterKey=randomBytes(32), resourcesDir=process.env.CANVAS_TEST_RESOURCES;
  let host;
  async function api(route,options={}) {
    const res=await fetch(host.origin+'/api'+route,{...options,headers:{Cookie:`open_ai_canvas_session=${host.sessionCookie}`,'Content-Type':'application/json',...options.headers}});
    const payload=await res.json();
    return {status:res.status,payload};
  }
  try {
    host=await startHost({resourcesDir,dataDir,masterKey});
    const bootstrap=(await api('/desktop/bootstrap')).payload;
    assert.equal(bootstrap.code,0);
    const owner=bootstrap.data.principal.id, profile=bootstrap.data.profileId;
    const health=(await api('/health')).payload.data;
    assert.equal(health.mode,'desktop');assert.equal(health.desktopSchema,3);
    assert.equal((await api('/agent/runs',{method:'POST'})).status,404);
    assert.equal((await api('/desktop/session',{method:'POST',headers:{Authorization:'Bearer reused'}})).status,401);
    const project={id:'desktop-smoke',revision:0,title:'中文画布',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString(),
      nodes:[{id:'text-1',type:'text',title:'文本',position:{x:20,y:30},width:340,height:240,metadata:{content:'本地主机保存验证'}}],connections:[],chatSessions:[],activeChatId:null,previsScenes:[]};
    const saved=await api('/canvas-projects/desktop-smoke',{method:'PUT',body:JSON.stringify({project})});
    assert.equal(saved.status,200,JSON.stringify(saved.payload));assert.equal(saved.payload.data.project.revision,1);
    const stale=await api('/canvas-projects/desktop-smoke',{method:'PUT',body:JSON.stringify({project})});
    assert.equal(stale.status,409);
    await host.stop();host=null;
    host=await startHost({resourcesDir,dataDir,masterKey});
    const restarted=(await api('/desktop/bootstrap')).payload.data;
    assert.equal(restarted.principal.id,owner);assert.equal(restarted.profileId,profile);
    const loaded=await api('/canvas-projects/desktop-smoke');
    assert.equal(loaded.payload.data.project.nodes[0].metadata.content,'本地主机保存验证');
    assert.equal(fs.existsSync(path.join(dataDir,'.settings-key')),false);
    console.log(JSON.stringify({evidence:'host-restart-cas',dataDir,ownerStable:true,revision:loaded.payload.data.project.revision}));
  } finally {if(host) await host.stop();masterKey.fill(0);}
});
