import {Link} from 'react-router';
import StorageSettingsPage from '@/pages/admin/settings/storage-settings-page';
export default function DesktopStoragePage(){return <div className="p-6"><Link to="/settings" className="text-sm text-muted-foreground">← 本地设置</Link><StorageSettingsPage/></div>}
