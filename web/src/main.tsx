import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import { installChunkRecovery } from "@/lib/chunk-recovery";
import { bootstrapAppearance } from "@/services/appearance-bootstrap";
import { isIsolatedDirectorRepro } from "@/lib/dev-repro";

installChunkRecovery();

// The public film entry checks its availability independently of workspace bootstrap.
if (/^\/welcome\/?$/.test(window.location.pathname)) void import("./welcome-application");
else {
    // The backend-free DEV lab must not make requests before AppProviders isolates it.
    const appearanceReady = isIsolatedDirectorRepro(import.meta.env.DEV, window.location.pathname) ? Promise.resolve() : bootstrapAppearance();
    // 外观解析先落定（成功或失败都放行）再载入应用：保留启动闸门，同时不让失败的外观请求变成未处理的拒绝。
    void appearanceReady.catch(() => undefined).then(() => import("./application"));
}
