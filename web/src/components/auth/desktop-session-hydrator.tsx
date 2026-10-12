import { useEffect, useState, type ReactNode } from 'react';
import { bootstrapDesktopSession } from '@/services/desktop/bootstrap';
import { FullScreenLoader } from '@/components/ui/aceternity/full-screen-loader';
import { useUserStore } from '@/stores/use-user-store';

export function DesktopSessionHydrator({children}:{children:ReactNode}) {
    const [ready,setReady]=useState(false);
    const [error,setError]=useState('');
    const [retry,setRetry]=useState(0);
    useEffect(()=>{
        const abort=new AbortController();setReady(false);setError('');
        bootstrapDesktopSession(abort.signal).then(()=>{if(!abort.signal.aborted)setReady(true)}).catch(error=>{
            if(abort.signal.aborted)return;
            useUserStore.getState().clearSession();
            setError(error instanceof Error ? error.message : '本地主机连接失败');
        });
        return()=>abort.abort();
    },[retry]);
    if(error)return <div className="flex h-dvh items-center justify-center"><div className="space-y-4"><p>无法连接本地画布：{error}</p><button className="rounded border px-4 py-2" onClick={()=>setRetry(value=>value+1)}>重新连接</button></div></div>;
    return ready ? children : <FullScreenLoader label="正在连接本地画布" detail="恢复项目、素材与模型配置" />;
}
