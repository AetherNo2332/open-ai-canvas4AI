'use strict';
const fs=require('node:fs');const path=require('node:path');const assert=require('node:assert/strict');
async function runJournal({window,host,artifactDir,reopen}){
 fs.mkdirSync(artifactDir,{recursive:true});const key='infinite-canvas:command-journal:smoke%3Aowner:canvas:smoke-canvas';const original=path.join(artifactDir,'native-journal-first.json');
 const bridge=await window.webContents.executeJavaScript(`({protocol:window.infiniteCanvasDesktop.protocol,journal:typeof window.infiniteCanvasDesktop.commandJournal.get})`);assert.deepEqual(bridge,{protocol:1,journal:'function'});
 if(!reopen){const record={operationId:'original-operation',documentEpoch:'original-epoch',origin:host.origin};await window.webContents.executeJavaScript(`window.infiniteCanvasDesktop.commandJournal.set(${JSON.stringify(key)},${JSON.stringify(record)})`);fs.writeFileSync(original,JSON.stringify(record,null,2))}
 const saved=await window.webContents.executeJavaScript(`window.infiniteCanvasDesktop.commandJournal.get(${JSON.stringify(key)})`);assert.deepEqual(saved,JSON.parse(fs.readFileSync(original,'utf8')));
 const forbidden=await window.webContents.executeJavaScript(`window.infiniteCanvasDesktop.commandJournal.get('../../private').then(()=>false,()=>true)`);assert.equal(forbidden,true);
 const evidence={phase:reopen?'reopened':'stored',currentOrigin:host.origin,originalOrigin:saved.origin,operationId:saved.operationId,documentEpoch:saved.documentEpoch,forbiddenKeyRejected:forbidden};fs.writeFileSync(path.join(artifactDir,reopen?'native-journal-reopened.json':'native-journal-stored.json'),JSON.stringify(evidence,null,2));console.log(JSON.stringify({type:'smoke',...evidence}));
}
module.exports={runJournal};
