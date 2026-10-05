import http from "node:http";

let blocked = false;
let mode = "text";
let active = 0;
let peak = 0;
const requests = [];
const waiting = new Set();
const json = (res, status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
const server = http.createServer(async (req,res) => {
  if (req.url === "/control") {
    let body="";for await (const chunk of req) body+=chunk;
    const control=body ? JSON.parse(body) : {};
    if (control.reset) {requests.length=0;peak=active;}
    if (control.mode) mode=control.mode;
    if (control.block !== undefined) blocked=control.block;
    if (!blocked) {for(const release of waiting) release();waiting.clear();}
    return json(res,200,{active,peak,count:requests.length,requests});
  }
  if (req.url === "/stats") return json(res,200,{active,peak,count:requests.length,requests});
  if (req.method !== "POST") return json(res,200,{data:[{id:"event-test-model"}]});
  let body="";for await (const chunk of req) body+=chunk;
  const payload=JSON.parse(body);
  // Enforce the upstream tool-message contract while exercising recovery.
  const calls=new Set();
  for(const message of payload.messages ?? []) {
    if(message.role==="assistant")for(const call of message.tool_calls ?? [])calls.add(call.id);
    if(message.role==="tool" && !calls.has(message.tool_call_id))return json(res,400,{error:{message:"tool result without preceding tool call"}});
  }
  active++;peak=Math.max(peak,active);
  const record={arrivedAt:Date.now(),model:payload.model,mode,messageCount:payload.messages?.length ?? 0};requests.push(record);
  let disconnected=false;
  res.on("close",()=>{disconnected=true;});
  try {
    if(blocked)await new Promise((resolve)=>{waiting.add(resolve);res.once("close",()=>{waiting.delete(resolve);resolve();});});
    if(disconnected)return;
    const names = new Map();
    let snapshotHash;
    let applied = false;
    for (const message of payload.messages ?? []) {
      for (const call of message.tool_calls ?? []) names.set(call.id,call.function?.name);
      if (message.role === "tool") {
        const name=names.get(message.tool_call_id);
        if(name === "canvas_apply_ops") applied=true;
        if(name === "canvas_get_state") {
          const text=typeof message.content === "string" ? message.content : message.content?.map(part=>part.text??"").join("");
          try {snapshotHash=JSON.parse(text).snapshotHash;} catch {}
        }
      }
    }
    const functionName=mode === "tool" && !applied ? (snapshotHash ? "canvas_apply_ops" : "canvas_get_state") : undefined;
    const args=functionName === "canvas_apply_ops" ? {snapshotHash,ops:[{type:"add_node",id:`stub_node_${requests.length}`,nodeType:"text",title:"隔离验收节点",content:"test",x:0,y:0}]} : {};
    const content=functionName ? null : "隔离验收回复：任务已完成。";
    const toolCalls=functionName ? [{id:`call_${requests.length}`,type:"function",function:{name:functionName,arguments:JSON.stringify(args)}}] : undefined;
    json(res,200,{id:`stub_${requests.length}`,object:"chat.completion",created:Math.floor(Date.now()/1000),model:payload.model,
      choices:[{index:0,message:{role:"assistant",content,...(toolCalls?{tool_calls:toolCalls}:{})},finish_reason:toolCalls?"tool_calls":"stop"}],
      usage:{prompt_tokens:100,completion_tokens:30,total_tokens:130}});
    record.completedAt=Date.now();
  }finally{active--;}
});
server.listen(8080,"0.0.0.0");
