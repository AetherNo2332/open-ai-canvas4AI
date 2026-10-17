'use strict';
const {test}=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs/promises');const os=require('node:os');const path=require('node:path');
const {createCommandJournalStore,authorizeJournalSender}=require('../command-journal.cjs');
test('native command journal survives store reopen independent of renderer port',async()=>{
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'canvas-command-journal-'));const key='infinite-canvas:command-journal:host%3Aowner:canvas';
 const one=createCommandJournalStore(root);await one.set(key,{protocolVersion:1,pending:[{operationId:'stable'}]});const two=createCommandJournalStore(root);assert.deepEqual(await two.get(key),{protocolVersion:1,pending:[{operationId:'stable'}]});await two.set(key,{protocolVersion:1,pending:[]});assert.deepEqual(await one.get(key),{protocolVersion:1,pending:[]});assert.equal(await one.get(key+'-missing'),null);
});
test('journal exposes only bounded app keys and the current top-level renderer',async()=>{
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'canvas-command-journal-'));const journal=createCommandJournalStore(root);
 await assert.rejects(journal.get('../../credentials'),/journal_key_invalid/);await assert.rejects(journal.set('infinite-canvas:command-journal:scope:canvas','x'.repeat(32*1024*1024)),/journal_value_too_large/);
 const mainFrame={url:'http://127.0.0.1:41001/canvas'};const contents={id:7,mainFrame};const event={sender:contents,senderFrame:mainFrame};assert.equal(authorizeJournalSender(event,contents,'http://127.0.0.1:41001'),true);
 for(const invalid of [{...event,sender:{id:8,mainFrame}},{...event,senderFrame:{url:mainFrame.url}},{...event,senderFrame:{url:'http://evil.invalid/canvas'}}])assert.throws(()=>authorizeJournalSender(invalid,contents,'http://127.0.0.1:41001'),/journal_sender_denied/);
});
