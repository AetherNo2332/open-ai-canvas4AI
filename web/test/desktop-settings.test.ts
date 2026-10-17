import {expect,test} from 'bun:test';
import {channelEditDraft,channelMutation} from '../src/services/desktop/settings-state';

test('editing a persisted channel never hydrates secrets into the renderer form',()=>{
 const draft=channelEditDraft({id:'channel',name:'模型服务',baseUrl:'https://models.example/v1',enabled:true,hasApiKey:true,apiKey:'accidental-secret',headers:[{name:'X-Secret',value:'accidental-header'}]} as any);
 expect(JSON.stringify(draft)).not.toContain('accidental');
 const update=channelMutation({...draft,name:'改名'},true);
 expect(update).not.toHaveProperty('apiKey');expect(update).not.toHaveProperty('headers');
});
test('a deliberate replacement credential is sent only with the mutation',()=>{
 const draft=channelEditDraft();draft.name='本地';draft.baseUrl='http://127.0.0.1:8000/v1';draft.apiKey='new-secret';
 expect(channelMutation(draft,false).apiKey).toBe('new-secret');
 expect(channelEditDraft({id:'saved',name:draft.name,baseUrl:draft.baseUrl,enabled:true,hasApiKey:true}).apiKey).toBe('');
});
