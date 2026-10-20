import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const source = (path: string) => readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8");

test("single-page registration retains the final backend validation and first-admin exception", () => {
    const page = source("pages/auth/register.tsx");
    expect(page).not.toContain("setStep(");
    expect(page).toContain("if (!settings?.firstUser && !verification.ticket)");
    expect(page).toContain("acceptedTerms: agreementAccepted");
    expect(page).toContain("if (password !== confirmPassword)");
    expect(page).toContain("if (Array.from(password).length < 8)");
    expect(page).toContain("if (registering.current || registerCountdown > 0) return;");
    expect(page).toContain("verificationUnavailable || submitting || previewEmailVerification");
});
