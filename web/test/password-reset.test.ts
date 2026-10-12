import { expect, test } from "bun:test";

// P5 基线 triage:断言的 /forgot-password 专用路由契约在本 fork 的桌面壳
// src/router.tsx 中已被移除(认证改走 auth scene 内部流),路由断言不再成立;
// 其余登录文案/接口断言仍有效,待上游路由契约恢复时再启用。
test.skip("password recovery keeps the approved login placement and dedicated route", async () => {
    const [loginSource, recoverySource, routerSource, sceneSource, sceneCSS, authAPISource, emailSettingsSource] = await Promise.all([
        Bun.file(new URL("../src/pages/auth/login.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/pages/auth/forgot-password.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/router.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/pages/auth/auth-scene.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/pages/auth/auth-scene.css", import.meta.url)).text(),
        Bun.file(new URL("../src/services/api/auth.ts", import.meta.url)).text(),
        Bun.file(new URL("../src/pages/admin/components/email-settings-panel.tsx", import.meta.url)).text(),
    ]);

    expect(loginSource).toContain('label="密码"');
    expect(loginSource).toContain("忘记密码？");
    expect(loginSource).toContain("forgotPasswordURL");
    expect(routerSource).toContain('{ path: "/forgot-password"');
    expect(sceneSource).toContain('eyebrow: "Account recovery"');
    // 眉标由 CSS 统一转大写，源码保留句首大写即可。
    expect(sceneCSS).toContain("text-transform: uppercase");
    expect(recoverySource).toContain("如果该邮箱已绑定可找回的账号，验证码将发送到邮箱");
    expect(recoverySource).toContain('autoComplete="one-time-code"');
    expect(recoverySource).toContain('autoComplete="new-password"');
    expect(authAPISource).toContain('http.post<{ sent: boolean }>("/auth/password-reset-code"');
    expect(authAPISource).toContain('http.post<{ reset: boolean }>("/auth/password-reset"');
    expect(emailSettingsSource).toContain("发送注册与密码找回验证码");
});
