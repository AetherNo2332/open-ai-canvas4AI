import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const source = (path: string) => readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8");

test("auth pages expose the reference layout landmarks", () => {
    const scene = source("pages/auth/auth-scene.tsx");
    const login = source("pages/auth/login.tsx");
    const register = source("pages/auth/register.tsx");

    expect(scene).toContain("auth-scene-help");
    expect(scene).toContain("register-password");
    expect(login).toContain("auth-login-remember");
    expect(login).toContain("auth-login-terms");
    expect(register).toContain("auth-register-step");
    expect(scene).toContain("为你的账户设置密码");
    expect(login).toContain('params.get("preview") === "email-verification"');
    expect(login).toContain('previewEmailVerification ? "email" : method');
    expect(register).toContain('params.get("preview") === "email-verification"');
    expect(register).toContain('purpose="register"');
});
