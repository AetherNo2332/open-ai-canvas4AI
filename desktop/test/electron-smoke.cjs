'use strict';
const fs=require('node:fs');
const path=require('node:path');
const {setTimeout:delay}=require('node:timers/promises');

async function runSmoke({window,host,artifactDir}) {
  fs.mkdirSync(artifactDir,{recursive:true});
  const state=()=>window.webContents.executeJavaScript(`({url:location.href,title:document.title,text:document.body.innerText,buttons:[...document.querySelectorAll('button')].map(b=>({text:b.innerText,label:b.getAttribute('aria-label'),title:b.title,disabled:b.disabled})),inputs:[...document.querySelectorAll('input')].map(i=>({type:i.type,accept:i.accept,placeholder:i.placeholder}))})`);
  let dom;
  for(let i=0;i<120;i++) {
    dom=await state();
    if(dom.buttons.some(b=>b.text.includes('新建画布')&&!b.disabled) || dom.text.includes('无法连接本地画布'))break;
    await delay(500);
  }
  const screenshot=await window.webContents.capturePage();
  fs.writeFileSync(path.join(artifactDir,'canvas-library.png'),screenshot.toPNG());
  fs.writeFileSync(path.join(artifactDir,'renderer-state.json'),JSON.stringify({versions:process.versions,dom},null,2));
  if(!dom.buttons.some(b=>b.text.includes('新建画布')&&!b.disabled))throw new Error('desktop_ui_not_ready');
  console.log(JSON.stringify({type:'smoke',phase:'canvas-library',instanceId:host.instanceId,artifactDir}));
  await window.webContents.executeJavaScript(`[...document.querySelectorAll('button')].find(b=>b.innerText.trim()==='新建画布'&&!b.disabled).click()`);
  for(let i=0;i<120;i++) {
    dom=await state();
    if(new URL(dom.url).pathname.startsWith('/canvas/') && dom.buttons.some(b=>b.title==='添加节点' || b.label==='添加节点'))break;
    if(dom.text.includes('读取失败'))break;
    await delay(500);
  }
  fs.writeFileSync(path.join(artifactDir,'canvas-created.png'),(await window.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(artifactDir,'editor-state.json'),JSON.stringify(dom,null,2));
  if(!new URL(dom.url).pathname.startsWith('/canvas/'))throw new Error('canvas_create_failed');
  await window.webContents.executeJavaScript(`[...document.querySelectorAll('button')].find(b=>b.innerText.trim()==='新建文本').click()`);
  const png=await window.webContents.executeJavaScript(`(()=>{const c=document.createElement('canvas');c.width=96;c.height=64;const x=c.getContext('2d');x.fillStyle='#c91838';x.fillRect(0,0,96,64);x.fillStyle='white';x.font='16px sans-serif';x.fillText('CANVAS',12,36);return c.toDataURL('image/png').split(',')[1]})()`);
  const fixture=path.join(artifactDir,'ui-fixture.png');fs.writeFileSync(fixture,Buffer.from(png,'base64'));
  window.webContents.debugger.attach('1.3');
  try {
    const {root}=await window.webContents.debugger.sendCommand('DOM.getDocument');
    const {nodeId}=await window.webContents.debugger.sendCommand('DOM.querySelector',{nodeId:root.nodeId,selector:'input[type="file"]'});
    await window.webContents.debugger.sendCommand('DOM.setFileInputFiles',{nodeId,files:[fixture]});
  } finally {window.webContents.debugger.detach();}
  const canvasId=new URL(dom.url).pathname.split('/').pop();
  let saved;
  for(let i=0;i<120;i++) {
    saved=await window.webContents.executeJavaScript(`fetch('/api/canvas-projects/'+${JSON.stringify(canvasId)},{credentials:'include'}).then(r=>r.json())`);
    const nodes=saved.data?.project?.nodes || [];
    if(nodes.some(n=>n.type==='text') && nodes.some(n=>n.type==='image'&&n.metadata?.storageKey?.startsWith('resource:')))break;
    await delay(500);
  }
  const project=saved.data?.project;
  if(!project?.nodes.some(n=>n.type==='text') || !project.nodes.some(n=>n.type==='image'&&n.metadata?.storageKey?.startsWith('resource:')))throw new Error('ui_nodes_not_persisted');
  const resource=project.nodes.find(n=>n.type==='image').metadata.storageKey.slice('resource:'.length);
  const media=await window.webContents.executeJavaScript(`fetch('/api/resources/'+${JSON.stringify(resource)}+'/file',{credentials:'include'}).then(async r=>({status:r.status,bytes:(await r.arrayBuffer()).byteLength}))`);
  if(media.status!==200 || media.bytes<100)throw new Error('ui_media_unreadable');
  fs.writeFileSync(path.join(artifactDir,'canvas-edited.png'),(await window.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(artifactDir,'saved-project.json'),JSON.stringify({canvasId,project,media},null,2));
  console.log(JSON.stringify({type:'smoke',phase:'canvas-nodes-persisted',canvasId,nodeCount:project.nodes.length,revision:project.revision}));
}
async function runReopen({window,host,artifactDir}) {
  const network=[];
  window.webContents.session.webRequest.onCompleted({urls:[`${host.origin}/api/*`]},details=>network.push({path:new URL(details.url).pathname,status:details.statusCode}));
  window.webContents.session.webRequest.onErrorOccurred({urls:['http://*/*']},details=>network.push({path:new URL(details.url).pathname,error:details.error}));
  const saved=JSON.parse(fs.readFileSync(path.join(artifactDir,'saved-project.json'),'utf8'));
  await window.loadURL(`${host.origin}/canvas/${saved.canvasId}`);
  let project;
  for(let i=0;i<120;i++) {
    const dom=await window.webContents.executeJavaScript(`({text:document.body.innerText,buttons:[...document.querySelectorAll('button')].map(b=>b.getAttribute('aria-label'))})`);
    if(dom.buttons.includes('添加节点')) {
      project=await window.webContents.executeJavaScript(`fetch('/api/canvas-projects/'+${JSON.stringify(saved.canvasId)}).then(r=>r.json()).then(p=>p.data.project)`);
      break;
    }
    await delay(500);
  }
  if(!project || project.nodes.length!==saved.project.nodes.length || project.revision!==saved.project.revision)throw new Error('canvas_restart_mismatch');
  const resource=project.nodes.find(n=>n.type==='image').metadata.storageKey.slice('resource:'.length);
  const media=await window.webContents.executeJavaScript(`fetch('/api/resources/'+${JSON.stringify(resource)}+'/file').then(async r=>({status:r.status,bytes:(await r.arrayBuffer()).byteLength}))`);
  if(media.status!==200 || media.bytes!==saved.media.bytes)throw new Error('media_restart_mismatch');
  // Give decoded media a chance to paint before recording visual evidence.
  await delay(1500);
  const images=await window.webContents.executeJavaScript(`[...document.images].map(i=>({path:i.src?new URL(i.src,location.href).pathname:'',complete:i.complete,width:i.naturalWidth}))`);
  const access=await window.webContents.executeJavaScript(`fetch('/api/resources/access',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify([{resourceId:${JSON.stringify(resource)},purpose:'display'}])}).then(r=>r.json()).then(d=>({code:d.code,path:d.data?.items?.[0]?.access?.url?new URL(d.data.items[0].access.url,location.href).pathname:null,error:d.data?.items?.[0]?.error}))`);
  fs.writeFileSync(path.join(artifactDir,'restart-media-state.json'),JSON.stringify({images,network,access},null,2));
  const captured=await window.webContents.capturePage();
  fs.writeFileSync(path.join(artifactDir,'canvas-reopened.png'),captured.toPNG());
  const bitmap=captured.toBitmap();let fixturePixels=0;
  for(let offset=0;offset<bitmap.length;offset+=4)if(bitmap[offset+2]>160 && bitmap[offset+1]<90 && bitmap[offset]<110)fixturePixels++;
  if(fixturePixels<4000)throw new Error(`canvas_restart_media_not_visible:${fixturePixels}`);
  fs.writeFileSync(path.join(artifactDir,'restart-evidence.json'),JSON.stringify({canvasId:saved.canvasId,instanceId:host.instanceId,nodeCount:project.nodes.length,revision:project.revision,media},null,2));
  console.log(JSON.stringify({type:'smoke',phase:'canvas-reopened',canvasId:saved.canvasId,nodeCount:project.nodes.length,revision:project.revision,media}));
}
module.exports={runSmoke,runReopen};
