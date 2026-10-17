const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const {randomBytes}=require('node:crypto');
const {spawn}=require('node:child_process');
const {startHost}=require('../runtime.cjs');

test('real host command receipt survives an abrupt process exit', {skip:!process.env.CANVAS_TEST_RESOURCES},async()=>{
 const dataDir=fs.mkdtempSync(path.join(os.tmpdir(),'canvas-command-中文 '));
 const resourcesDir=process.env.CANVAS_TEST_RESOURCES,masterKey=randomBytes(32);
 let host,child;
 const launch=()=>startHost({resourcesDir,dataDir,masterKey,spawnProcess:(...args)=>(child=spawn(...args))});
 const api=async(route,options={})=>{
  const response=await fetch(host.origin+'/api'+route,{...options,headers:{Cookie:`open_ai_canvas_session=${host.sessionCookie}`,'Content-Type':'application/json',...options.headers}});
  return {status:response.status,body:await response.json()};
 };
 try {
  host=await launch();
  const created=await api('/desktop/canvases',{method:'POST',body:JSON.stringify({operationId:'create1',clientId:'integration',title:'命令重启验收'})});
  assert.equal(created.status,200,JSON.stringify(created.body));
  const snapshot=created.body.data;
  const request={protocolVersion:1,canvasId:snapshot.canvasId,documentEpoch:snapshot.documentEpoch,operationId:'persist1',clientId:'integration',baseRevision:snapshot.revision,operations:[{type:'add_node',id:'text1',nodeType:'text',content:'进程退出后仍可读取'}]};
  const route=`/desktop/canvases/${snapshot.canvasId}/commands`;
  const first=await api(route,{method:'POST',body:JSON.stringify(request)});assert.equal(first.status,200,JSON.stringify(first.body));
  child.kill();await host.exited;host=null;
  host=await launch();
  const repeated=await api(route,{method:'POST',body:JSON.stringify(request)});assert.equal(repeated.status,200,JSON.stringify(repeated.body));assert.deepEqual(repeated.body,first.body);
  const loaded=await api(`/desktop/canvases/${snapshot.canvasId}/snapshot`);assert.equal(loaded.body.data.documentEpoch,snapshot.documentEpoch);assert.equal(loaded.body.data.document.nodes[0].metadata.content,'进程退出后仍可读取');
  const events=await api(`/desktop/canvases/${snapshot.canvasId}/events?after=0`);assert.equal(events.body.data.length,1);
  console.log(JSON.stringify({evidence:'host-command-abrupt-restart',dataDir,revision:loaded.body.data.revision,events:events.body.data.length,receiptStable:true}));
 }finally {if(host)await host.stop();masterKey.fill(0);}
});
