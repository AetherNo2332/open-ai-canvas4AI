import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { App } from "antd";
import RegisterPage from "../src/pages/auth/register";
import { VerificationFields } from "../src/components/auth/verification-fields";
import { emptyVerification } from "../src/services/api/verification";

test("registration renders account, contact, code and password on one page even with legacy step links", () => {
    for (const url of ["/register", "/register?step=2", "/register?step=3"]) {
        const html = renderToStaticMarkup(
            <MemoryRouter initialEntries={[url]}>
                <App>
                    <RegisterPage />
                </App>
            </MemoryRouter>,
        );
        for (const label of ["用户名", "显示名称", "邮箱", "邮件验证码", "密码", "确认密码", "完成注册"]) expect(html).toContain(label);
        expect(html).toContain('placeholder="3-32位字符"');
        expect(html).toContain('placeholder="不填则使用用户名"');
        expect(html).not.toContain("下一步");
        expect(html.indexOf("邮件验证码")).toBeLessThan(html.indexOf("确认密码"));
        expect(html.match(/type="email"/g)?.length).toBe(1);
        const pattern = html.match(/pattern="([^\"]+)"/)?.[1];
        expect(pattern).toBeDefined();
        const username = new RegExp(`^(?:${pattern})$`, "v");
        for (const valid of ["abc", "user_1", "user-name", "a".repeat(32)]) expect(username.test(valid)).toBe(true);
        for (const invalid of ["ab", "a".repeat(33), "用户名", "has space", "bad@email"]) expect(username.test(invalid)).toBe(false);
    }
});

test("registration requires a complete code while login verification keeps its existing input contract", () => {
    const render = (purpose: "register" | "login") =>
        renderToStaticMarkup(
            <App>
                <VerificationFields purpose={purpose} method="email" value={{ ...emptyVerification, email: "test@example.com" }} onChange={() => {}} />
            </App>,
        );
    const register = render("register");
    expect(register).toMatch(/<input[^>]*pattern="\[0-9\]\{6\}"[^>]*>/);
    expect(register).toMatch(/<input[^>]*required=""[^>]*pattern="\[0-9\]\{6\}"|<input[^>]*pattern="\[0-9\]\{6\}"[^>]*required=""/);
    expect(render("login")).not.toContain('pattern="[0-9]{6}"');
});
