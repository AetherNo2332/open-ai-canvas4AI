import { Tabs } from "antd";
import { ArrowLeft } from "lucide-react";
import { Link, Outlet, useLocation, useNavigate } from "react-router";

import { BrandLogo } from "@/components/brand/brand-logo";
import { SiteComplianceFooter } from "@/components/layout/site-compliance-footer";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import { useThemeStore } from "@/stores/use-theme-store";

import "./auth-scene.css";

const AUTH_TABS = [
    { key: "login", label: "登录" },
    { key: "register", label: "注册" },
];

const authCopy = {
    login: {
        eyebrow: "Welcome back",
        title: "进入创作现场",
        description: "继续编辑你的画布、素材与生成任务。",
    },
    register: {
        eyebrow: "Create account",
        title: "建立你的创作空间",
        description: "一个账号管理画布、素材、技能和模型偏好。",
    },
    recovery: {
        eyebrow: "Account recovery",
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
    const location = useLocation();
    const navigate = useNavigate();
    const recovery = location.pathname === "/forgot-password";
    const activeTab = location.pathname === "/register" ? "register" : "login";
    const copy = recovery ? authCopy.recovery : activeTab === "register" ? authCopy.register : authCopy.login;
    const heroStatement = appearance.authHeroTitle.trim();
    const heroDescription = appearance.authHeroDescription.trim();
    const hasMedia = Boolean(appearance.authVideoUrl || appearance.authVideoPosterUrl);

    return (
        <main className="auth-scene">
            <header className="auth-scene-masthead">
                <Link to="/" className="auth-scene-brand">
                    <BrandLogo theme={theme} className="auth-scene-mark" alt="" fallback={<span className="auth-scene-mark-fallback" />} />
                    {appearance.brandName}
                </Link>
                <Link to="/" className="auth-scene-return">
                    <ArrowLeft className="size-3.5" />
                    返回首页
                </Link>
            </header>

            <div className="auth-scene-body">
                <section className={hasMedia ? "auth-scene-statement is-over-media" : "auth-scene-statement"} aria-label={copy.title}>
                    {hasMedia ? (
                        <div className="auth-scene-media" aria-hidden="true">
                            {appearance.authVideoUrl ? (
                                <video className="auth-scene-media-source" src={appearance.authVideoUrl} poster={appearance.authVideoPosterUrl || undefined} autoPlay={appearance.authVideoAutoplay} muted loop playsInline preload="metadata" />
                            ) : (
                                <img className="auth-scene-media-source" src={appearance.authVideoPosterUrl} alt="" decoding="async" />
                            )}
                            <span className="auth-scene-media-scrim" />
                        </div>
                    ) : null}

                    <div className="auth-scene-statement-copy">
                        <p className="auth-scene-eyebrow">{copy.eyebrow}</p>
                        <h1 className="auth-scene-title">{heroStatement || copy.title}</h1>
                        <p className="auth-scene-summary">{heroDescription || copy.description}</p>
                    </div>
                </section>

                <section className="auth-scene-form" aria-label={copy.title}>
                    <div className="auth-scene-form-inner">
                        <h2 className="auth-scene-form-title">{copy.title}</h2>
                        {!recovery ? <Tabs className="auth-scene-tabs" activeKey={activeTab} items={AUTH_TABS} onChange={(key) => navigate({ pathname: key === "register" ? "/register" : "/login", search: location.search })} /> : null}
                        <div key={location.pathname} className="auth-scene-form-body">
                            <Outlet />
                        </div>
                    </div>
                </section>
            </div>

            <SiteComplianceFooter variant="auth" className="auth-scene-footer" />
        </main>
    );
}
