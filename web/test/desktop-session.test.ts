import { describe, expect, test } from 'bun:test';
import { createDesktopSessionController } from '../src/services/desktop/session-controller';

const session = (profileId: string, principalId: string) => ({instanceId:'instance',profileId,principal:{id:principalId,ownerId:principalId,clientId:'',role:'owner',scopes:['canvas.read']},features:{},drawingEngine:{},runtimeLimits:{},capabilities:{protocol:1,saveMode:'legacy-single-writer'}});

describe('desktop session boundary',()=>{
  test('auth failure leaves no user or guest fallback',async()=>{
    let user:unknown={id:'old'};
    const controller=createDesktopSessionController({readBootstrap:async()=>session('p','u'),readAuth:async()=>{throw new Error('unauthorized')},clear:async()=>{user=null},apply:async payload=>{user=payload.user}});
    await expect(controller.connect({endpoint:'http://127.0.0.1:1',kind:'local'})).rejects.toThrow('unauthorized');
    expect(user).toBeNull();expect(controller.current()).toBeNull();
  });
  test('switching hosts clears prior projects and forbids legacy autosync installation',async()=>{
    let profile='host-a';let cachedProjects=['old-project'];let appliedOptions:unknown;
    const controller=createDesktopSessionController({readBootstrap:async()=>session(profile,'u'),readAuth:async()=>({user:{id:'u'}}),clear:async()=>{cachedProjects=[]},apply:async(_payload,options)=>{appliedOptions=options}});
    await controller.connect({endpoint:'http://127.0.0.1:1',kind:'local'});
    cachedProjects=['a-project'];profile='host-b';
    await controller.connect({endpoint:'http://127.0.0.1:2',kind:'local'});
    expect(cachedProjects).toEqual([]);
    expect(appliedOptions).toEqual({storageScope:'host-b:u',remoteAutoSync:false,awaitHydration:true});
  });
  test('identity mismatch cannot open the workspace',async()=>{
    let applied=false;
    const controller=createDesktopSessionController({readBootstrap:async()=>session('p','u'),readAuth:async()=>({user:{id:'other'}}),clear:async()=>{},apply:async()=>{applied=true}});
    await expect(controller.connect({endpoint:'http://127.0.0.1:1',kind:'local'})).rejects.toThrow('desktop_identity_mismatch');
    expect(applied).toBe(false);
  });
});
