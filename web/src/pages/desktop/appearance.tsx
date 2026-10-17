import {useEffect,useState} from 'react';
import {App,Button,Input,Alert} from 'antd';
import {Link} from 'react-router';
import {SkinThemeEditor} from '@/pages/admin/settings/components/skin-theme-editor';
import {DEFAULT_CLASSIC_SKIN,type SkinDefinition} from '@/lib/skin-themes';
import {http} from '@/services/api/request';
import type {AdminAppearance} from '@/services/api/appearance';
import {commitPublicAppearance} from '@/stores/use-appearance-store';
import '@/styles/admin-ui.css';
import '@/pages/admin/theme/admin-tokens.css';

export default function DesktopAppearancePage(){
    const {message}=App.useApp();
    const [setting,setSetting]=useState<AdminAppearance|null>(null),[skins,setSkins]=useState<SkinDefinition[]>([]),[selected,setSelected]=useState('classic'),[brand,setBrand]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState(false);
    useEffect(()=>{void http.get<{setting:AdminAppearance}>('/desktop/settings/appearance').then(({setting})=>{setSetting(setting);setSkins(setting.skinThemes);setSelected(setting.skinId);setBrand(setting.brandName)}).catch(e=>setError(e.message))},[]);
    const duplicate=(source:SkinDefinition)=>{if(skins.length>=16)return;const skin={...structuredClone(source),id:crypto.randomUUID(),name:source.name+' 副本',locked:false};setSkins([...skins,skin]);setSelected(skin.id)};
    const save=async()=>{setBusy(true);try{const {setting}=await http.patch<{setting:AdminAppearance}>('/desktop/settings/appearance',{brandName:brand,skinId:selected,skinThemes:skins});setSetting(setting);commitPublicAppearance(setting.public);message.success('外观已保存')}catch(e){message.error(e instanceof Error?e.message:'保存失败')}finally{setBusy(false)}};
    return <main className="admin-content mx-auto max-w-6xl space-y-6 p-8"><Link to="/settings" className="text-sm text-muted-foreground">← 本地设置</Link><div className="flex items-center justify-between"><h1 className="text-2xl font-semibold">外观</h1><Button type="primary" disabled={!setting} loading={busy} onClick={()=>void save()}>保存外观</Button></div>{error&&<Alert type="error" title={error}/>}<label className="block space-y-2"><span className="text-sm">工作区名称</span><Input aria-label="工作区名称" value={brand} onChange={e=>setBrand(e.target.value)}/></label><SkinThemeEditor themes={skins} selectedID={selected} disabled={busy} onSelect={setSelected} onCreate={()=>duplicate(DEFAULT_CLASSIC_SKIN)} onDuplicate={id=>{const source=skins.find(s=>s.id===id);if(source)duplicate(source)}} onDelete={id=>{if(skins.find(s=>s.id===id)?.locked)return;setSkins(skins.filter(s=>s.id!==id));if(selected===id)setSelected('classic')}} onChange={skin=>setSkins(skins.map(s=>s.id===skin.id?skin:s))}/><p className="text-sm text-muted-foreground">单个画布的背景与节点样式仍可在画布工具栏中调整。</p></main>
}
