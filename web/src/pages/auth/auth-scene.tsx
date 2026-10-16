import { Link, Outlet, useLocation, useSearchParams } from "react-router";
import { useEffect } from "react";
import { Moon, Sun } from "lucide-react";

import { BrandLogo } from "@/components/brand/brand-logo";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import { useThemeStore } from "@/stores/use-theme-store";
import { useAuthMotion } from "./use-auth-motion";
import { useAuthAmbientMotion } from "./use-auth-ambient-motion";

import "./auth-scene.css";

const authCopy = {
    login: {
        title: "登录",
        description: "欢迎回来，继续你的创作。",
    },
    register: {
        title: "创建你的账户",
        description: "从这里开始，让灵感成为作品。",
    },
    recovery: {
        title: "重新设置密码",
        description: "验证账号邮箱后，设置一个新的登录密码。",
    },
} as const;

export function LinuxDOIcon() {
    return (
        <span
            aria-hidden
            className="size-5 shrink-0 rounded-full"
            style={{
                background: "linear-gradient(to bottom, #1d1d1f 0 33.333%, #efefef 33.333% 66.666%, #feb005 66.666% 100%)",
                boxShadow: "0 0 0 1px rgba(255,255,255,.14)",
            }}
        />
    );
}

export function AuthScene() {
    const appearance = useAppearanceStore((state) => state.appearance);
    const theme = useThemeStore((state) => state.theme);
    const setTheme = useThemeStore((state) => state.setTheme);
    const { sceneRoot } = useAuthAmbientMotion(theme);
    const location = useLocation();
    const [searchParams] = useSearchParams();
    const recovery = location.pathname === "/forgot-password";
    const activeTab = location.pathname === "/register" ? "register" : "login";
    const registerPasswordStep = activeTab === "register" && searchParams.get("step") === "2";
    const registerVerificationStep = activeTab === "register" && searchParams.get("step") === "3";
    const authMode = recovery ? "recovery" : registerVerificationStep ? "register-verification" : registerPasswordStep ? "register-password" : activeTab;
    const { motionRoot, onValidationError } = useAuthMotion(authMode);
    const copy = recovery ? authCopy.recovery : activeTab === "register" ? authCopy.register : authCopy.login;
    const heroDescription = appearance.authHeroDescription.trim();
    const title = activeTab === "login" && !recovery ? `登录到 ${appearance.brandName}` : registerVerificationStep ? "验证你的邮箱" : registerPasswordStep ? "为你的账户设置密码" : copy.title;
    const switchSearch = new URLSearchParams(location.search);
    switchSearch.delete("step");

    useEffect(() => {
        const applyPillRadius = () => {
            const controls = document.querySelectorAll<HTMLElement>(".auth-scene-form-body .ant-input, .auth-scene-form-body .ant-input-affix-wrapper, .auth-scene-form-body .ant-btn, .auth-scene-form-body .auth-code-field");
            controls.forEach((control) => {
                const isPasswordInnerInput = control.matches(":is(.ant-input-affix-wrapper, .auth-code-field) > .ant-input");
                control.style.setProperty("border-radius", isPasswordInnerInput ? "0" : "999px", "important");
                if (isPasswordInnerInput) {
                    control.style.setProperty("background", "inherit", "important");
                    control.style.setProperty("border", "0", "important");
                    control.style.setProperty("box-shadow", "none", "important");
                }
            });
        };
        applyPillRadius();
        const observer = new MutationObserver(applyPillRadius);
        observer.observe(document.body, { childList: true, subtree: true });
        return () => observer.disconnect();
    }, [authMode]);

    return (
        <main className="auth-scene" ref={sceneRoot}>
            <button type="button" className="auth-theme-toggle" aria-label={theme === "dark" ? "切换到浅色模式" : "切换到深色模式"} onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
                {theme === "dark" ? <Sun size={20} aria-hidden="true" /> : <Moon size={20} aria-hidden="true" />}
            </button>
            <div className="auth-scene-body">
                <section className="auth-scene-form" aria-label={title}>
                    <div className="auth-scene-form-inner">
                        <header className="auth-scene-heading">
                            <Link to="/" className="auth-scene-brand" aria-label={`${appearance.brandName} 首页`}>
                                <BrandLogo theme={theme} className="auth-scene-mark" alt="" fallback={<span className="auth-scene-mark auth-scene-mark-fallback" />} />
                            </Link>
                            <h1 className="auth-scene-title">{title}</h1>
                            <p className="auth-scene-summary">{heroDescription || copy.description}</p>
                        </header>
                        <div
                            ref={motionRoot}
                            className="auth-scene-form-body auth-scene-form-transition"
                            data-auth-mode={authMode}
                        >
                            <Outlet context={{ onValidationError }} />
                        </div>
                        {!recovery ? (
                            <p className="auth-scene-switch">
                                <span>{activeTab === "register" ? "已有账户？" : "还没有账户？"}</span>
                                <Link className="auth-scene-link" to={{ pathname: activeTab === "register" ? "/login" : "/register", search: `?${switchSearch.toString()}` }}>
                                    {activeTab === "register" ? "返回登录" : "立即注册"}
                                </Link>
                            </p>
                        ) : null}
                    </div>
                </section>
            </div>

            <div className={`auth-scene-footer-shell${registerPasswordStep ? " is-register-password" : ""}`}>
                {registerPasswordStep ? <span className="auth-scene-footer-brand">{appearance.brandName}</span> : <span aria-hidden="true" />}
                <footer className="site-compliance-footer auth-scene-footer" aria-label="站点版权与备案信息">
                    <span>© 2026 {appearance.brandName}</span>
                </footer>
                <a className="auth-scene-help" href="mailto:support@example.com">需要帮助?</a>
            </div>
        </main>
    );
}
