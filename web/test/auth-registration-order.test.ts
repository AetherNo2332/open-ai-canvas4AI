import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const source = (path: string) => readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8");

test("registration requires password and agreement before verification", () => {
    const page = source("pages/auth/register.tsx");
    const scene = source("pages/auth/auth-scene.tsx");

    expect(page).toContain("type RegisterStep = 1 | 2 | 3;");
    expect(page).toContain("setStep(3);");
    expect(page).toContain("step === 3 && methods.length > 0 ? <VerificationFields");
    expect(page).toContain("if (step === 3 && !settings?.firstUser && !verification.ticket)");
    expect(page).not.toContain("step === 1 && previewEmailVerification ? <VerificationFields");
    expect(scene).toContain("registerVerificationStep");
    expect(scene).toContain("验证你的邮箱");

    const passwordStep = page.indexOf("auth-register-passwords");
    const verificationStep = page.indexOf("step === 3 && methods.length > 0 ? <VerificationFields");
    expect(passwordStep).toBeGreaterThanOrEqual(0);
    expect(verificationStep).toBeGreaterThan(passwordStep);
});
