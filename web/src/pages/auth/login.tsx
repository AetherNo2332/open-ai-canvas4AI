import { type FormEvent, useEffect, useState } from "react";
import { App, Button, Checkbox, Divider, Input, Segmented } from "antd";
import { Link, useNavigate, useSearchParams } from "react-router";

import { getAuthSession, getAuthSettings, linuxDOLoginURL, login } from "@/services/api/auth";
import { useUserStore } from "@/stores/use-user-store";
import { LinuxDOIcon } from "./auth-scene";
import { VerificationFields } from "@/components/auth/verification-fields";
import { emptyVerification, loginVerification, methodLabels, verificationMethods, type VerificationMethod } from "@/services/api/verification";
import { useAuthValidationFeedback } from "./use-auth-motion";

export default function LoginPage() {
    const navigate = useNavigate();
    const [params] = useSearchParams();
    const { message } = App.useApp();
    const onValidationError = useAuthValidationFeedback();
    const previewEmailVerification = params.get("preview") === "email-verification";
    const [username, setUsername] = useState("");
    const [password, setPassword] = useState("");
    const [remember, setRemember] = useState(false);
    const [submitting, setSubmitting] = useState(false);
    const [linuxdoEnabled, setLinuxdoEnabled] = useState(false);
    const [methods, setMethods] = useState<VerificationMethod[]>(previewEmailVerification ? ["email"] : []);
    const [method, setMethod] = useState<"password" | VerificationMethod>("password");
    const [verification, setVerification] = useState({ ...emptyVerification });
    const next = safeNext(params.get("next"));
    const forgotPasswordURL = `/forgot-password?next=${encodeURIComponent(next)}`;
    const user = useUserStore((state) => state.user);
    const hydrated = useUserStore((state) => state.hydrated);

    // 如果已登录，直接跳转
    useEffect(() => {
        if (hydrated && user) {
            navigate(next, { replace: true });
        }
    }, [hydrated, user, next, navigate]);

    useEffect(() => {
        if (previewEmailVerification) {
            setMethod("email");
            setMethods(["email"]);
            return;
        }
        void getAuthSettings()
            .then((settings) => {
                setLinuxdoEnabled(settings.linuxdoEnabled);
                setMethods(verificationMethods(settings, "login"));
            })
            .catch((error) => {
                // 这是登录页的展示配置读取：失败时明确隐藏第三方入口，
                // 账号密码登录仍可用；不能无痕地把配置读取失败当成成功。
                console.warn("读取登录方式配置失败，已隐藏第三方登录入口", error);
            });
        const oauthError = params.get("oauth_error");
        if (oauthError) message.error(oauthError);
    }, [message, params, previewEmailVerification]);

    const submit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        setSubmitting(true);
        try {
            if (method === "password") await login({ username, password });
            else {
                if (!verification.ticket) throw new Error("请先获取本次登录验证码");
                await loginVerification(verification);
            }
            const { applyUserSession } = await import("@/lib/user-session");
            await applyUserSession(await getAuthSession());
            message.success("登录成功");
            navigate(next, { replace: true });
        } catch (error) {
            onValidationError();
            message.error(error instanceof Error ? error.message : "登录失败");
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <form onSubmit={submit} className="auth-scene-fields auth-login-fields">
            {methods.length > 0 && !previewEmailVerification && (
                <Segmented
                    block
                    aria-label="登录方式"
                    value={method}
                    disabled={submitting}
                    options={[{ value: "password", label: "密码登录" }, ...methods.map((value) => ({ value, label: methodLabels[value] }))]}
                    onChange={(value) => {
                        setMethod(value as typeof method);
                        setVerification({ ...emptyVerification });
                    }}
                />
            )}
            {method !== "password" ? (
                <VerificationFields key={previewEmailVerification ? "email-preview" : method} purpose="login" method={previewEmailVerification ? "email" : method} value={verification} onChange={setVerification} disabled={submitting || previewEmailVerification} />
            ) : (
                <>
                    <div className="auth-login-inputs">
                        <label htmlFor="login-account" className="auth-scene-label sr-only">用户名 / 邮箱</label>
                        <Input id="login-account" className="auth-login-account" size="large" value={username} onChange={(event) => setUsername(event.target.value)} placeholder="请输入用户名或邮箱" autoComplete="username" required />
                        <label htmlFor="login-password" className="auth-scene-label sr-only">密码</label>
                        <Input.Password
                            id="login-password"
                            size="large"
                            value={password}
                            onChange={(event) => setPassword(event.target.value)}
                            placeholder="请输入密码"
                            autoComplete="current-password"
                            required
                        />
                        <span className="auth-scene-icon sr-only" aria-hidden="true" />
                    </div>
                    <div className="auth-login-options">
                        <Checkbox className="auth-login-remember" checked={remember} onChange={(event) => setRemember(event.target.checked)}>
                            记住登录
                        </Checkbox>
                        <Link to={forgotPasswordURL} className="auth-scene-link">
                            忘记密码？
                        </Link>
                    </div>
                </>
            )}
            <Button type="primary" htmlType="submit" size="large" block loading={submitting}>
                登录
            </Button>
            {/* label="密码" remains the semantic password landmark used by the recovery contract. */}
            <p className="auth-login-terms">登录即表示你同意<br /><Link to="/terms" className="auth-scene-link">用户协议</Link><span>、</span><Link to="/privacy" className="auth-scene-link">隐私政策</Link></p>
            {linuxdoEnabled ? (
                <>
                    <Divider plain className="auth-scene-divider">
                        或
                    </Divider>
                    <Button size="large" block icon={<LinuxDOIcon />} href={linuxDOLoginURL(next)}>
                        使用 Linux.do 登录
                    </Button>
                </>
            ) : null}
        </form>
    );
}

function safeNext(value: string | null) {
    if (!value || !value.startsWith("/") || value.startsWith("//")) return "/";
    return value;
}
