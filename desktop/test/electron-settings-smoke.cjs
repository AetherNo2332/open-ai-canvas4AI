'use strict';
const fs=require('node:fs');const path=require('node:path');const http=require('node:http');
const {setTimeout:delay}=require('node:timers/promises');

async function runSettings({window,host,artifactDir,reopen=false}){
  fs.mkdirSync(artifactDir,{recursive:true});
  const statePath=path.join(artifactDir,'settings-state.json');
  const evaluate=code=>window.webContents.executeJavaScript(code);
  const wait=async(predicate,label)=>{for(let i=0;i<100;i++){if(await evaluate(predicate))return;await delay(200)}throw new Error(`settings_ui_timeout:${label}`)};
  const click=async(text)=>{await wait(`[...document.querySelectorAll('button')].some(b=>b.innerText.trim()===${JSON.stringify(text)}&&!b.disabled)`,text);await evaluate(`[...document.querySelectorAll('button')].find(b=>b.innerText.trim()===${JSON.stringify(text)}&&!b.disabled).click()`);await delay(150)};
  const fill=async(selector,value)=>evaluate(`(()=>{const input=document.querySelector(${JSON.stringify(selector)});if(!input)throw new Error('input missing');Object.getOwnPropertyDescriptor(input.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(value)});input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  await window.loadURL(host.origin+'/settings/models');
  await wait(`document.body.innerText.includes('允许访问的本机与局域网模型')`,'models-page');
  if(reopen){
    const saved=JSON.parse(fs.readFileSync(statePath,'utf8'));
    await wait(`document.body.innerText.includes(${JSON.stringify(saved.name)})`,'channel-restart');
    const channels=await evaluate(`fetch('/api/desktop/settings/channels').then(r=>r.json()).then(r=>r.data.channels)`);
    const item=channels.find(c=>c.id===saved.channelId);
    if(!item?.hasApiKey||!item.models.some(m=>m.id===saved.modelId&&m.enabled))throw new Error('settings_restart_mismatch');
    if(JSON.stringify(channels).includes('ui-test-key'))throw new Error('settings_ui_secret_leaked');
    await window.loadURL(host.origin+'/settings/appearance');
    await wait(`document.querySelector('input[aria-label="工作区名称"]')?.value==='桌面验收工作区'`,'appearance-restart');
    await wait(`!!document.querySelector('.admin-skin-workspace')`,'skin-editor');await delay(500);
    fs.writeFileSync(path.join(artifactDir,'appearance-reopened.png'),(await window.webContents.capturePage()).toPNG());
    await window.loadURL(host.origin+'/settings/storage');
    await wait(`document.body.innerText.includes('1. 选择新资源存储位置')`,'storage-loaded');await delay(500);
    fs.writeFileSync(path.join(artifactDir,'storage-configured.png'),(await window.webContents.capturePage()).toPNG());
    console.log(JSON.stringify({type:'settings-smoke',phase:'reopened',channelId:item.id,modelId:saved.modelId}));return;
  }
  let calls=0;
  const stub=http.createServer(async(req,res)=>{
    if(req.headers.authorization!=='Bearer ui-test-key'){res.writeHead(401);res.end();return}
    if(req.url==='/v1/models'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({data:[{id:'desktop-stub-text'}]}));return}
    if(req.url==='/v1/chat/completions'){calls++;res.setHeader('Content-Type','application/json');res.end(JSON.stringify({choices:[{message:{role:'assistant',content:'desktop model fixture answer'}}],usage:{prompt_tokens:1,completion_tokens:1,total_tokens:2}}));return}
    res.writeHead(404);res.end();
  });
  await new Promise(resolve=>stub.listen(0,'127.0.0.1',resolve));
  try{
    await fill('textarea[aria-label="可信模型主机"]','127.0.0.1');await click('保存主机授权');
    const name='模型设置验收 '+Date.now();
    await click('添加模型服务');await fill('input[aria-label="服务名称"]',name);await fill('input[aria-label="模型服务地址"]',`http://127.0.0.1:${stub.address().port}/v1`);await fill('input[type="password"]','ui-test-key');await click('保存服务');
    await wait(`!document.querySelector('input[aria-label="服务名称"]')`,'service-save');
    await wait(`[...document.querySelectorAll('section')].some(s=>s.querySelector('h2')?.innerText===${JSON.stringify(name)})`,'saved-channel');
    await evaluate(`[...document.querySelectorAll('section')].find(s=>s.querySelector('h2')?.innerText===${JSON.stringify(name)}).querySelectorAll('button')[1].click()`);await delay(150);
    await fill('input[aria-label="模型标识"]','desktop-stub-text');
    await click('测试调用');
    for(let i=0;i<100 && calls===0;i++)await delay(100);
    await wait(`[...document.querySelectorAll('button')].some(b=>b.innerText.trim()==='保存模型'&&!b.disabled&&!b.classList.contains('ant-btn-loading'))`,'model-test');
    if(calls!==1)throw new Error('settings_model_test_did_not_reach_provider');
    await click('保存模型');await wait(`!document.querySelector('input[aria-label="模型标识"]')`,'model-save');
    const channels=await evaluate(`fetch('/api/desktop/settings/channels').then(r=>r.json()).then(r=>r.data.channels)`);
    const item=channels.find(c=>c.name===name),model=item?.models.find(m=>m.modelKey==='desktop-stub-text');
    if(!item?.hasApiKey||!model?.enabled||JSON.stringify(channels).includes('ui-test-key')){
      fs.writeFileSync(path.join(artifactDir,'save-failure.json'),JSON.stringify(channels.map(c=>({id:c.id,name:c.name,hasApiKey:c.hasApiKey,models:c.models.map(m=>({id:m.id,key:m.modelKey,enabled:m.enabled,protocol:m.protocol}))})),null,2));
      throw new Error('settings_ui_save_failed');
    }
    const catalog=await evaluate(`fetch('/api/model-catalog').then(r=>r.json()).then(r=>r.data)`);
    if(!catalog.channels.some(c=>c.id===item.id&&c.models.some(m=>m.modelKey==='desktop-stub-text'&&m.available)))throw new Error('settings_model_missing_from_canvas_catalog');
    fs.writeFileSync(path.join(artifactDir,'models-configured.png'),(await window.webContents.capturePage()).toPNG());
    await window.loadURL(host.origin+'/settings/appearance');await wait(`!!document.querySelector('input[aria-label="工作区名称"]')`,'appearance');await fill('input[aria-label="工作区名称"]','桌面验收工作区');await click('保存外观');
    await wait(`document.title==='桌面验收工作区' || document.body.innerText.includes('外观已保存')`,'appearance-save');
    await window.loadURL(host.origin+'/settings/storage');await wait(`document.body.innerText.includes('1. 选择新资源存储位置')`,'storage');await delay(500);
    fs.writeFileSync(path.join(artifactDir,'storage-configured.png'),(await window.webContents.capturePage()).toPNG());
    fs.writeFileSync(statePath,JSON.stringify({name,channelId:item.id,modelId:model.id,calls,hasApiKey:item.hasApiKey},null,2));
    console.log(JSON.stringify({type:'settings-smoke',phase:'saved',channelId:item.id,modelId:model.id,calls}));
  }catch(error){
    fs.writeFileSync(path.join(artifactDir,'settings-failure.png'),(await window.webContents.capturePage()).toPNG());
    fs.writeFileSync(path.join(artifactDir,'settings-failure.txt'),await evaluate('document.body.innerText'));
    throw error;
  }finally{await new Promise(resolve=>stub.close(resolve))}
}
module.exports={runSettings};
