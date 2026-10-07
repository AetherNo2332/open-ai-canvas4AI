import assert from 'node:assert/strict';
import test from 'node:test';
import { CanvasBridge, CanvasLeaseLost, CanvasRunTerminated, type PiSnapshot } from '../src/bridge.js';
import { runLeasedPiSession, workerErrorSummary } from '../src/worker-runtime.js';

const run: PiSnapshot = {
  runId:'run-runtime', userId:'user-runtime', revision:1, status:'running', piSessionLeaseEpoch:7,
  request:{prompt:'private'}, canonical:{systemPrompt:'',messages:[],tools:[],toolChoice:'auto'},
  modelLimits:{contextWindowTokens:128000,maxOutputTokens:16384,configured:false,source:'default'},tools:[],
};
for (const responseLost of [false,true]) {
  test(`failure reporting recovers from ${responseLost ? 'committed response loss' : 'one 503'} without duplicate terminal mutation`, async () => {
    const original=globalThis.fetch;
    let status='running', attempts=0, mutations=0;
    globalThis.fetch=(async (url,init) => {
      const headers=init?.headers as Record<string,string>;
      assert.equal(headers['X-Agent-Session-Epoch'],'7');
      if(String(url).endsWith('/fail')) {
        attempts++;
        assert.doesNotMatch(String(init?.body),/private/);
        if(attempts===1 && !responseLost) return new Response('{}',{status:503});
        if(status==='running') {status='failed';mutations++;}
        if(attempts===1 && responseLost) throw new TypeError('response lost');
        return new Response(JSON.stringify({code:0,data:{failed:true}}));
      }
      return new Response(JSON.stringify({code:0,data:{run:{...run,status}}}));
    }) as typeof fetch;
    try {
      const error=new TypeError('private credentials');
      await assert.rejects(runLeasedPiSession(new CanvasBridge('http://backend','token','worker'),run,new AbortController().signal,
        async()=>{throw error;}), e=>e===error);
      assert.equal(status,'failed');assert.equal(mutations,1);assert.equal(attempts,responseLost?1:2);
    } finally {globalThis.fetch=original;}
  });
}
test('persistent failure-report outage is bounded and explicitly unconfirmed',async()=>{
  const original=globalThis.fetch;
  let attempts=0;
  globalThis.fetch=(async(url)=>{
    if(String(url).endsWith('/fail'))attempts++;
    return new Response('{}',{status:503});
  }) as typeof fetch;
  try{
    await assert.rejects(runLeasedPiSession(new CanvasBridge('http://backend','token','worker'),run,new AbortController().signal,
      async()=>{throw new Error('private');}),/终态上报未确认/);
    assert.equal(attempts,3);
  }finally{globalThis.fetch=original;}
});
test('terminal, lost-lease and shutdown control never overwrite the authoritative run',async()=>{
  for(const error of [new CanvasRunTerminated('cancelled'),new CanvasLeaseLost('old epoch')]){
    let calls=0;
    await runLeasedPiSession({failRun:async()=>{calls++;}},run,new AbortController().signal,async()=>{throw error;});
    assert.equal(calls,0);
  }
  const controller=new AbortController();controller.abort();
  let calls=0;
  await runLeasedPiSession({failRun:async()=>{calls++;}},run,controller.signal,async()=>{throw new Error('shutdown');});
  assert.equal(calls,0);
  assert.doesNotMatch(workerErrorSummary(new Error('prompt=private password=secret')),/private|secret/);
});
