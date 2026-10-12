import {expect,test} from "bun:test";
import {canCancelGenerationTask} from "@/lib/generation-task-display";

test("local previs remains cancellable while rendering and stops after terminal",()=>{
 const task={type:"previs_render",status:"running" as const,stage:"预演：rendering",progress:40};
 expect(canCancelGenerationTask(task)).toBe(true);
 expect(canCancelGenerationTask({...task,status:"succeeded"})).toBe(false);
});
