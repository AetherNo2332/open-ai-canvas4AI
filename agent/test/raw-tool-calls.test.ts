import assert from "node:assert/strict";
import test from "node:test";
import { Agent } from "@earendil-works/pi-agent-core";
import { createAssistantMessageEventStream, type AssistantMessage, type Model } from "@earendil-works/pi-ai";
import { Unsafe, type TSchema } from "typebox";
import { canvasToolCallToRaw, createCanvasToolCall, prepareCanvasToolArguments } from "../src/raw-tool-calls.js";

const model:Model<any>={id:"test",name:"test",api:"openai-completions",provider:"canvas",baseUrl:"http://unused",
  reasoning:false,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:8192,maxTokens:2048};
const usage={input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}};

test("cloned durable annotations are rehydrated before Pi prepares a zero-argument tool",async()=>{
  const raw='{"cookie":"private-cookie"';
  const restored=structuredClone(createCanvasToolCall({id:"bad",type:"function",function:{name:"zero",arguments:raw}}));
  let requests=0,executions=0;
  const agent=new Agent({initialState:{model,tools:[{name:"zero",description:"zero",label:"zero",
    parameters:Unsafe<TSchema>({type:"object",properties:{},additionalProperties:false}),
    prepareArguments:prepareCanvasToolArguments,
    execute:async()=>{executions++;return {content:[{type:"text",text:"unexpected"}],details:null};}}]},
    streamFn:()=>{
      const stream=createAssistantMessageEventStream();
      const message:AssistantMessage={role:"assistant",content:requests++===0?[restored]:[{type:"text",text:"correct the rejected call"}],
        api:model.api,provider:model.provider,model:model.id,usage,stopReason:requests===1?"toolUse":"stop",timestamp:Date.now()};
      stream.push({type:"start",partial:message});
      stream.push({type:"done",reason:message.stopReason as "stop"|"toolUse",message});stream.end(message);
      return stream;
    }});
  agent.subscribe(event=>{
    if(event.type==="message_end"&&event.message.role==="assistant")
      event.message.content.filter(part=>part.type==="toolCall").forEach(canvasToolCallToRaw);
  });
  await agent.prompt("perform the tool call");
  assert.equal(requests,2);
  assert.equal(executions,0);
  const result=agent.state.messages.find(message=>message.role==="toolResult");
  assert.ok(result?.role==="toolResult"&&result.isError);
  assert.doesNotMatch(JSON.stringify(result),/private-cookie/);
  assert.equal(canvasToolCallToRaw(restored).function.arguments,raw);
});

test("legal model objects cannot impersonate an outer raw-argument annotation",()=>{
  for(const args of [{__canvasInvalidToolArguments:true},{canvasInvalidArguments:{rawArguments:'{"bad"'},__canvasInvalidToolArguments:true}]){
    const raw=JSON.stringify(args);
    const call=createCanvasToolCall({id:"valid",type:"function",function:{name:"permissive",arguments:raw}});
    assert.deepEqual(prepareCanvasToolArguments(call.arguments),args);
    assert.equal(canvasToolCallToRaw(structuredClone(call)).function.arguments,raw);
  }
});
