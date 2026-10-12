'use strict';
const fs=require('node:fs/promises');const path=require('node:path');const {createHash,randomUUID}=require('node:crypto');
const maxBytes=32*1024*1024;
function journalKey(key){
 if(typeof key!=='string'||key.length>2048||!/^infinite-canvas:command-journal:[A-Za-z0-9_%!~*'().:-]+$/.test(key))throw new Error('journal_key_invalid');
 // The digest is a bounded filename, not a content verification pass.
 return createHash('sha256').update(key).digest('hex')+'.json';
}
function createCommandJournalStore(dataDir){
 const root=path.resolve(dataDir,'command-journal');const queues=new Map();
 async function directory(){await fs.mkdir(root,{recursive:true,mode:0o700});const info=await fs.lstat(root);if(!info.isDirectory()||info.isSymbolicLink())throw new Error('journal_directory_invalid')}
 return {
  async get(key){
   const file=path.join(root,journalKey(key));await queues.get(key)?.catch(()=>{});await directory();
   let handle;try{const info=await fs.lstat(file);if(!info.isFile()||info.isSymbolicLink())throw new Error('journal_file_invalid');handle=await fs.open(file,'r');const stat=await handle.stat();if(stat.size>maxBytes)throw new Error('journal_value_too_large');const envelope=JSON.parse(await handle.readFile('utf8'));if(envelope.version!==1||envelope.key!==key||!Object.hasOwn(envelope,'value'))throw new Error('journal_file_invalid');return envelope.value}
   catch(error){if(error.code==='ENOENT')return null;throw error}finally{await handle?.close()}
  },
  async set(key,value){
   const file=path.join(root,journalKey(key));const body=JSON.stringify({version:1,key,value});if(!body||!Object.hasOwn(JSON.parse(body),'value'))throw new Error('journal_value_invalid');if(Buffer.byteLength(body)>maxBytes)throw new Error('journal_value_too_large');
   const previous=queues.get(key);const write=(async()=>{
    await previous?.catch(()=>{});await directory();const temporary=file+'.tmp-'+randomUUID();let handle;
    try{handle=await fs.open(temporary,'wx',0o600);await handle.writeFile(body,'utf8');await handle.sync();await handle.close();handle=undefined;await fs.rename(temporary,file)}
    catch(error){await handle?.close();await fs.unlink(temporary).catch(()=>{});throw error}
   })();queues.set(key,write);try{await write}finally{if(queues.get(key)===write)queues.delete(key)}
  },
 };
}
function authorizeJournalSender(event,contents,origin){
 let senderOrigin;try{senderOrigin=new URL(event.senderFrame?.url||'').origin}catch{}
 if(!contents||event.sender!==contents||event.senderFrame!==contents.mainFrame||senderOrigin!==origin)throw new Error('journal_sender_denied');return true;
}
module.exports={createCommandJournalStore,authorizeJournalSender};
