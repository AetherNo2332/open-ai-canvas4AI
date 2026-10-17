export type ChannelIdentity = {id:string;name:string;baseUrl:string;enabled:boolean;hasApiKey?:boolean};
export type ChannelDraft = {name:string;baseUrl:string;enabled:boolean;apiKey:string;secretKey:string;headersJSON:string};
export function channelEditDraft(channel?:ChannelIdentity):ChannelDraft {
    return {name:channel?.name||'',baseUrl:channel?.baseUrl||'',enabled:channel?.enabled??true,apiKey:'',secretKey:'',headersJSON:''};
}
export function channelMutation(draft:ChannelDraft,editing:boolean) {
    return {name:draft.name,baseUrl:draft.baseUrl,enabled:draft.enabled,
        ...(draft.apiKey?{apiKey:draft.apiKey}:{}),...(draft.secretKey?{secretKey:draft.secretKey}:{}),
        ...(draft.headersJSON.trim()?{headers:JSON.parse(draft.headersJSON)}:{}),...(!editing?{models:[]}:{}),
    };
}
