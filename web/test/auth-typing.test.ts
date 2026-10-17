import { expect, test } from "bun:test";
import { insertedAuthText, permitsAuthTyping } from "../src/pages/auth/auth-typing";

test("typing animation isolates inserted text while preserving surrounding text", () => {
    expect(insertedAuthText("alice", "alice@example.com")).toEqual({ before: "alice", inserted: "@example.com", after: "" });
    expect(insertedAuthText("ac", "abc")).toEqual({ before: "a", inserted: "b", after: "c" });
    expect(insertedAuthText("你好", "你们好")).toEqual({ before: "你", inserted: "们", after: "好" });
    expect(insertedAuthText("🙂", "🙃")).toEqual({ before: "", inserted: "🙃", after: "" });
    expect(insertedAuthText("abc", "ab")).toBeNull();
    expect(insertedAuthText("same", "same")).toBeNull();
});

test("passwords stay out of the visual text layer, including revealed passwords", () => {
    expect(permitsAuthTyping("text", "username", false)).toBe(true);
    expect(permitsAuthTyping("email", "email", false)).toBe(true);
    expect(permitsAuthTyping("password", "", false)).toBe(false);
    expect(permitsAuthTyping("text", "new-password", false)).toBe(false);
    expect(permitsAuthTyping("text", "current-password", false)).toBe(false);
    expect(permitsAuthTyping("text", "", true)).toBe(false);
});
