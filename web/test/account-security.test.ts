import { expect, test } from "bun:test";

test("settings exposes a self-service password change that keeps the current session", async () => {
    const [settingsSource, paneSource, authAPISource] = await Promise.all([
        Bun.file(new URL("../src/pages/settings/index.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/pages/settings/account-security-pane.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/services/api/auth.ts", import.meta.url)).text(),
    ]);

    // 改密入口挂在设置页的「账户安全」分区，而不是只依赖管理员后台重置。
    expect(settingsSource).toContain("account-security-pane");
    expect(settingsSource).toContain('{ key: "account", label: "账户安全"');
    expect(settingsSource).toContain("account: <SettingsPane><AccountSecurityPane /></SettingsPane>");
    expect(settingsSource).toContain('"storage" | "account" | "diagnostics"');

    // 表单必须同时要求当前密码与两次新密码，并交给浏览器密码管理器识别。
    expect(paneSource).toContain('name="currentPassword"');
    expect(paneSource).toContain('autoComplete="current-password"');
    expect(paneSource).toContain('name="newPassword"');
    expect(paneSource).toContain("密码至少 8 位");
    expect(paneSource).toContain('name="confirmPassword"');
    expect(paneSource).toContain("两次输入的新密码不一致");
    expect(paneSource).toContain("changePassword({");
    expect(paneSource).toContain("其他设备需要重新登录");

    expect(authAPISource).toContain('http.post<{ changed: boolean }>("/auth/password"');
});
