export interface AgentSchedulerSetting {
 dispatchConcurrency:number;
 maxResidentSessions:number;
 maxResidentPerCanvas:number;
 revision:number;
}

export class RuntimeConfigController {
 private value:AgentSchedulerSetting;
 constructor(initial:AgentSchedulerSetting) {this.validate(initial);this.value={...initial};}
 get current():Readonly<AgentSchedulerSetting> {return this.value;}
 apply(next:AgentSchedulerSetting):boolean {
  this.validate(next);
  if(next.revision<=this.value.revision)return false;
  this.value={...next};return true;
 }
 private validate(p:AgentSchedulerSetting):void {
  if(![p.dispatchConcurrency,p.maxResidentSessions,p.maxResidentPerCanvas,p.revision].every(Number.isSafeInteger)||p.dispatchConcurrency<1||p.dispatchConcurrency>16||p.maxResidentSessions<p.dispatchConcurrency||p.maxResidentSessions>64||p.maxResidentPerCanvas<1||p.maxResidentPerCanvas>p.maxResidentSessions||p.revision<0)throw new Error("Invalid Agent scheduler configuration");
 }
}

export interface CapacityReport {
 active:number;capacity:number;claimReservations:number;dispatchActive:number;
 readyQueued:number;draining:boolean;appliedConfigRevision:number;
}
