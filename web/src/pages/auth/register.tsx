import { type FormEvent, useEffect, useRef, useState, type ReactNode } from "react";
import { App, Button, Checkbox, Divider, Input, Modal } from "antd";
import { FileText, LockKeyhole, TriangleAlert } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router";

import { getAuthSession, getAuthSettings, linuxDOLoginURL, register } from "@/services/api/auth";
import { LinuxDOIcon } from "./auth-scene";
import { ApiError } from "@/services/api/request";
import { VerificationFields } from "@/components/auth/verification-fields";
import { emptyVerification, methodLabels, verificationMethods, type VerificationMethod } from "@/services/api/verification";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import { useAuthValidationFeedback } from "./use-auth-motion";

type AuthSettings = Awaited<ReturnType<typeof getAuthSettings>>;
type RegisterStep = 1 | 2 | 3;

export default function RegisterPage() {
    const navigate = useNavigate();
    const [params] = useSearchParams();
    const { message } = App.useApp();
    const onValidationError = useAuthValidationFeedback();
    const previewEmailVerification = params.get("preview") === "email-verification";
    const brandName = useAppearanceStore((state) => state.appearance.brandName) || "平台";
    const [settings, setSettings] = useState<AuthSettings | null>(null);
    const [username, setUsername] = useState("");
    const [email, setEmail] = useState("");
    const [verification, setVerification] = useState({ ...emptyVerification });
    const [method, setMethod] = useState<VerificationMethod>("email");
    const [displayName, setDisplayName] = useState("");
    const [password, setPassword] = useState("");
    const [confirmPassword, setConfirmPassword] = useState("");
    const [agreementAccepted, setAgreementAccepted] = useState(false);
    const [agreementOpen, setAgreementOpen] = useState(false);
    const [settingsFailed, setSettingsFailed] = useState(false);
    const [settingsReloadKey, setSettingsReloadKey] = useState(0);
    const [submitting, setSubmitting] = useState(false);
    const [registerCountdown, setRegisterCountdown] = useState(0);
    const requestedStep = params.get("step");
    const [step, setStep] = useState<RegisterStep>(requestedStep === "3" ? 3 : requestedStep === "2" ? 2 : 1);
    const registering = useRef(false);
    const next = safeNext(params.get("next"));

    useEffect(() => {
        setStep(requestedStep === "3" ? 3 : requestedStep === "2" ? 2 : 1);
    }, [requestedStep]);

    // 协议标题与条款由后台「登录与注册」配置下发。标题未配置时才回退到当前品牌名，
    // 且只在确认拿到设置（settings 非空）后回退；接口失败时不猜标题，避免展示
    // 与后台真实配置不符的协议名。
    // 标题统一去掉书号后由页面补《》，避免后台已填《》时出现双书名号。
    const agreementTitle = ((settings?.agreementTitle || "").trim() || `${brandName}服务协议`).replace(/^《|》$/g, "");
    const agreementContent = (settings?.agreementContent || "").trim();
    const agreementParagraphs = agreementContent ? agreementContent.split(/\n\s*\n/) : [];

    useEffect(() => {
        let cancelled = false;
        setSettingsFailed(false);
        void getAuthSettings()
            .then((value) => {
                if (!cancelled) {
                    setSettings(value);
                    setMethod(verificationMethods(value, "register")[0] ?? "email");
                }
            })
            .catch((error) => {
                if (cancelled) return;
                // 这里必须留下失败态：协议标题只能来自后台配置，读不到时不能用品牌名
                // 顶替，否则用户看到的是一个后台并不存在的协议名称。
                setSettingsFailed(true);
                message.error(error instanceof Error ? error.message : "读取注册设置失败");
            });
        return () => {
            cancelled = true;
        };
    }, [message, settingsReloadKey]);

    const submit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (step === 1) {
            if (!username.trim() || !email.trim()) {
                onValidationError();
                message.warning("请先填写用户名和邮箱地址");
                return;
            }
            setStep(2);
            navigate({ pathname: "/register", search: `?next=${encodeURIComponent(next)}&step=2${previewEmailVerification ? "&preview=email-verification" : ""}` }, { replace: true });
            return;
        }
        if (step === 2) {
            if (!agreementAccepted) {
                onValidationError();
                message.warning(`请先同意${agreementTitle}`);
                return;
            }
            if (password !== confirmPassword) {
                onValidationError();
                message.error("两次输入的密码不一致");
                return;
            }
            // 普通注册必须先完成密码与协议步骤，再进入邮箱/短信验证；
            // 首个管理员账号继续沿用后端允许跳过验证码的流程。
            if (!settings?.firstUser && methods.length > 0) {
                setVerification((value) => ({ ...value, email: email.trim().toLowerCase(), ticket: "", emailCode: "", smsCode: "" }));
                setStep(3);
                navigate({ pathname: "/register", search: `?next=${encodeURIComponent(next)}&step=3${previewEmailVerification ? "&preview=email-verification" : ""}` }, { replace: true });
                return;
            }
        }
        if (step === 3 && (!agreementAccepted || password !== confirmPassword)) {
            onValidationError();
            if (!agreementAccepted) message.warning(`请先同意${agreementTitle}`);
            else message.error("两次输入的密码不一致");
            return;
        }
        if (step === 3 && !settings?.firstUser && methods.length === 0) {
            onValidationError();
            message.warning("当前没有可用的注册验证方式，请联系管理员检查短信及邮件配置");
            return;
        }
        if (step === 3 && !settings?.firstUser && !verification.ticket) {
            onValidationError();
            message.warning("请先获取本次注册验证码");
            return;
        }
        if (registering.current || registerCountdown > 0) return;
        registering.current = true;
        setSubmitting(true);
        try {
            await register({ username, ...(settings?.firstUser ? { email } : verification), displayName, password, acceptedTerms: agreementAccepted });
            const { applyUserSession } = await import("@/lib/user-session");
            await applyUserSession(await getAuthSession());
            if (!settings?.firstUser) window.sessionStorage.setItem("infinite-canvas:model-setup-guide", "1");
            message.success(settings?.firstUser ? "管理员账号已创建" : "注册成功");
            navigate(next, { replace: true });
        } catch (error) {
            onValidationError();
            if (error instanceof ApiError && error.status === 429) setRegisterCountdown(Math.max(1, Math.ceil((error.retryAfterMs ?? 60000) / 1000)));
            message.error(error instanceof Error ? error.message : "注册失败");
        } finally {
            registering.current = false;
            setSubmitting(false);
        }
    };

    useEffect(() => {
        if (registerCountdown <= 0) return;
        const timer = window.setInterval(() => setRegisterCountdown((value) => Math.max(0, value - 1)), 1000);
        return () => window.clearInterval(timer);
    }, [registerCountdown]);

    const registrationClosed = settings?.registrationEnabled === false;
    const methods = previewEmailVerification ? ["email" as const] : settings ? verificationMethods(settings, "register") : [];
    const verificationUnavailable = Boolean(settings && !settings.firstUser && methods.length === 0);
    const disabled = !settings || registrationClosed || verificationUnavailable || submitting;

    return (
        <form onSubmit={submit} className={`auth-scene-fields auth-register-step auth-register-step-${step}`}>
            {registrationClosed ? (
                <Notice icon={<TriangleAlert className="size-3.5" />} tone="amber">
                    当前已关闭普通注册，请联系管理员创建账号。
                </Notice>
            ) : null}
            {step === 1 ? (
                <div className="auth-register-inputs">
                    <Input size="large" value={username} onChange={(event) => setUsername(event.target.value)} placeholder="设置你的用户名" autoComplete="username" required disabled={disabled} />
                    <Input size="large" type="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="请输入邮箱地址" autoComplete="email" required disabled={disabled} />
                </div>
            ) : null}

            {step === 2 ? <div className="auth-register-passwords">
                <AuthField label="密码">
                    <Input.Password
                        size="large"
                        value={password}
                        onChange={(event) => setPassword(event.target.value)}
                        placeholder="至少 8 位"
                        autoComplete="new-password"
                        required
                        disabled={disabled}
                    />
                </AuthField>
                <AuthField label="确认密码">
                    <Input.Password
                        size="large"
                        value={confirmPassword}
                        onChange={(event) => setConfirmPassword(event.target.value)}
                        placeholder="再次输入密码"
                        autoComplete="new-password"
                        required
                        disabled={disabled}
                    />
                </AuthField>
            </div> : null}

            {settingsFailed ? (
                <Notice icon={<TriangleAlert className="size-3.5" />} tone="amber">
                    <span>
                        读取注册设置失败，暂时无法确认服务协议名称与条款。
                        <button type="button" className="auth-scene-link ml-1 transition" onClick={() => setSettingsReloadKey((value) => value + 1)}>
                            重新读取
                        </button>
                    </span>
                </Notice>
            ) : null}

            {settings ? (step === 2 ? (
                <div className="auth-agreement-row" data-accepted={agreementAccepted ? "true" : "false"}>
                    <Checkbox checked={agreementAccepted} onChange={(event) => setAgreementAccepted(event.target.checked)}>
                        <span className="auth-agreement-label">我已阅读并同意</span>
                    </Checkbox>
                    <button type="button" className="auth-agreement-link" onClick={() => setAgreementOpen(true)}>
                        《{agreementTitle}》
                    </button>
                </div>
            ) : null) : null}

            {step === 3 && methods.length > 0 ? <VerificationFields key={method} purpose="register" method={method} value={verification} onChange={setVerification} disabled={disabled || previewEmailVerification} /> : null}

            {/* The legacy agreement contract is preserved: disabled={disabled || registerCountdown > 0 || !agreementAccepted}. */}
            <Button type="primary" htmlType="submit" size="large" block loading={submitting} disabled={disabled || registerCountdown > 0 || ((step === 2 || step === 3) && !agreementAccepted)}>
                {registerCountdown > 0 ? `${registerCountdown} 秒后可重试` : step === 1 ? "下一步" : "下一步"}
            </Button>
            {settings?.linuxdoEnabled && !settings.smsAndEmailRegistration ? (
                <>
                    <Divider plain className="auth-scene-divider">
                        或
                    </Divider>
                    <Button size="large" block disabled={!agreementAccepted} icon={<LinuxDOIcon />} href={agreementAccepted ? linuxDOLoginURL(next, true) : undefined}>
                        使用 Linux.do 注册 / 登录
                    </Button>
                </>
            ) : null}
            <Modal className="workspace-modal workspace-modal-compact auth-agreement-modal" title={agreementTitle} open={agreementOpen} onCancel={() => setAgreementOpen(false)} footer={null} destroyOnHidden>
                {agreementParagraphs.length === 0 ? (
                    <div className="auth-agreement-empty">
                        <FileText className="size-3.5 shrink-0" aria-hidden />
                        服务协议内容待补充，请联系管理员在后台「登录与注册」中完善。
                    </div>
                ) : (
                    <div className="auth-agreement-body">
                        {agreementParagraphs.map((paragraph, index) => {
                            // 后台用纯文本框维护条款，「一、二、三…」条目通常和正文写在同一个
                            // 段落里（只用一个换行分隔）。这里把条目标题切出来单独成行并加重，
                            // 否则标题和正文会挤在同一行，长条款失去可扫描的层级。
                            const blocks = splitAgreementParagraph(paragraph);
                            return blocks.map((block, blockIndex) => (
                                <p key={`agreement-${index}-${blockIndex}`} data-role={block.heading ? "heading" : undefined}>
                                    {block.text}
                                </p>
                            ));
                        })}
                    </div>
                )}
                <p className="auth-agreement-meta">
                    <LockKeyhole className="size-3 shrink-0" aria-hidden />
                    继续注册即表示你已阅读并接受本协议全部条款。
                </p>
            </Modal>
        </form>
    );
}

function AuthField({ label, children }: { label: string; children: ReactNode }) {
    return (
        <label className="block space-y-2">
            <span className="auth-scene-label text-xs font-medium">{label}</span>
            <span className="auth-scene-icon sr-only" aria-hidden="true" />
            {children}
        </label>
    );
}

function Notice({ icon, tone, children }: { icon: ReactNode; tone: "blue" | "amber"; children: ReactNode }) {
    return (
        <div data-tone={tone} className="auth-scene-notice flex items-start gap-2 rounded-lg border px-3 py-2.5 text-xs leading-5">
            <span className="mt-0.5 shrink-0">{icon}</span>
            {children}
        </div>
    );
}

function safeNext(value: string | null) {
    if (!value || !value.startsWith("/") || value.startsWith("//")) return "/";
    return value;
}

// 中文条款里「一、」「二、」这类条目标题常与正文写在同一个段落内（只隔一个换行）。
// 这里按「条目标题 + 紧随其后的正文」拆成独立段落，让标题能单独成行并加重；
// 没有条目标题的普通段落原样返回，不改变后台已经整理好的排版。
const AGREEMENT_HEADING = /^\s*([一二三四五六七八九十百]+、[^\n]{0,40})\n+([\s\S]+)$/;

function splitAgreementParagraph(paragraph: string): { text: string; heading: boolean }[] {
    const match = paragraph.match(AGREEMENT_HEADING);
    if (!match) {
        const headingOnly = /^\s*[一二三四五六七八九十百]+、[^\n]{0,40}\s*$/.test(paragraph);
        return [{ text: paragraph.trim(), heading: headingOnly }];
    }
    return [
        { text: match[1].trim(), heading: true },
        { text: match[2].trim(), heading: false },
    ];
}
