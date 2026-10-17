import { useUserStore } from "@/stores/use-user-store";
import { currentDesktopSession } from "@/services/desktop/bootstrap";
import { Link } from "react-router";

export default function DesktopSettingsPage() {
    const user = useUserStore((state) => state.user);
    const session = currentDesktopSession();
    return (
        <main className="mx-auto max-w-3xl space-y-6 p-8">
            <h1 className="text-2xl font-semibold">本地设置</h1>
            <p className="text-muted-foreground">项目与素材保存在当前设备，由本地主机管理。</p>
            <nav className="grid grid-cols-2 gap-3">
                <Link className="rounded-lg border p-4" to="/settings/models">
                    模型服务
                </Link>
                <Link className="rounded-lg border p-4" to="/plugins">
                    插件
                </Link>
                <Link className="rounded-lg border p-4" to="/settings/storage">
                    素材存储
                </Link>
                <Link className="rounded-lg border p-4" to="/settings/appearance">
                    外观
                </Link>
            </nav>
            <dl className="grid grid-cols-[9rem_1fr] gap-3">
                <dt>工作区</dt>
                <dd>{user?.displayName}</dd>
                <dt>本地主体</dt>
                <dd className="break-all font-mono text-sm">{session?.principal.id}</dd>
                <dt>保存方式</dt>
                <dd>本地主机版本校验</dd>
            </dl>
        </main>
    );
}
