import assert from "node:assert/strict";
import test from "node:test";
import { RuntimeConfigController } from "../src/runtime-config.js";
import { EventScheduler,RunEvents,runEventSessions } from "../src/event-scheduler.js";
import type { PiSnapshot } from "../src/bridge.js";

const tick=()=>new Promise<void>(resolve=>setImmediate(resolve));
test("hot slot changes expand immediately and shrink without cancelling in-flight work",async()=>{
 const s=new EventScheduler(4);let entered=0;let release!:()=>void;
 const gate=new Promise<void>(resolve=>release=resolve);
 const work=Array.from({length:10},()=>s.step("canvas",async()=>{entered++;await gate}));
 await tick();assert.equal(entered,4);
 s.setConcurrency(8);await tick();assert.equal(entered,8);
 s.setConcurrency(2);await tick();assert.equal(entered,8);
 release();await Promise.all(work);assert.equal(entered,10);assert.equal(s.active,0);
});
test("configuration only applies valid newer revisions",()=>{
 const c=new RuntimeConfigController({dispatchConcurrency:4,maxResidentSessions:64,maxResidentPerCanvas:16,revision:1});
 assert.equal(c.apply({...c.current,dispatchConcurrency:8,revision:2}),true);
 assert.equal(c.apply({...c.current,dispatchConcurrency:4,revision:1}),false);
 assert.throws(()=>c.apply({...c.current,maxResidentSessions:2,revision:3}),/Invalid/);
 assert.equal(c.current.dispatchConcurrency,8);
});
test("resident shrink rechecks claims already in flight and releases excess leases",async()=>{
 const events=new RunEvents();const stop=new AbortController();let cap=4;let claimed=0;let entered=0;let released=0;
 let returnClaims!:()=>void;const claims=new Promise<void>(resolve=>returnClaims=resolve);
 let finish!:()=>void;const running=new Promise<void>(resolve=>finish=resolve);
 const loop=runEventSessions({concurrency:4,maxSessions:4,events,signal:stop.signal,
  getConfig:()=>({dispatchConcurrency:4,maxResidentSessions:cap}),
  createBridge:()=>({claim:async()=>{const n=++claimed;if(n>4)return null;await claims;return {runId:`r${n}`,userId:"u",piSessionId:`s${n}`,request:{canvasId:"c"}} as PiSnapshot},failRun:async()=>{},release:async()=>{released++}}),
  run:async()=>{entered++;await running},onError:()=>{}});
 await tick();cap=1;returnClaims();await tick();await tick();
 assert.equal(entered,1);assert.equal(released,3);
 stop.abort();finish();await loop;
});

test("canvas quota shrink rechecks pending claims without shrinking process capacity",async()=>{
 const events=new RunEvents();const stop=new AbortController();let quota=4;let claimed=0;let entered=0;let released=0;
 let resolveClaims!:()=>void;const claims=new Promise<void>(resolve=>resolveClaims=resolve);
 let finish!:()=>void;const running=new Promise<void>(resolve=>finish=resolve);
 const loop=runEventSessions({concurrency:4,maxSessions:4,events,signal:stop.signal,
  getConfig:()=>({dispatchConcurrency:4,maxResidentSessions:4,maxResidentPerCanvas:quota}),
  createBridge:()=>({claim:async()=>{const n=++claimed;if(n>4)return null;await claims;return {runId:`c${n}`,userId:"u",piSessionId:`s${n}`,request:{canvasId:"same"}} as PiSnapshot},failRun:async()=>{},release:async()=>{released++}}),
  run:async()=>{entered++;await running},onError:()=>{}});
 await tick();quota=1;resolveClaims();await tick();await tick();
 try {assert.equal(entered,1);assert.equal(released,3);}finally{stop.abort();finish();await loop;}
});
