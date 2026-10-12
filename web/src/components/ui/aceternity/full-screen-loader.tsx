import { useEffect, useState } from "react";

import { BrandLogoFrame } from "@/components/brand/brand-logo";
import { cn } from "@/lib/utils";

type FullScreenLoaderProps = {
    label?: string;
    detail?: string;
    className?: string;
};

export function FullScreenLoader({ label = "正在恢复工作区", detail = "同步账号、模型和项目数据", className }: FullScreenLoaderProps) {
    return (
        <div data-full-screen-loader role="status" aria-live="polite" aria-label={`${label}，${detail}`} className={cn("full-screen-loader", className)}>
            <div className="full-screen-loader-panel">
                <div className="full-screen-loader-head">
                    <span className="full-screen-loader-mark">
                        <BrandLogoFrame className="full-screen-loader-mark-image" logoClassName="size-full" alt="" fallback={<span className="full-screen-loader-mark-fallback" />} />
                    </span>
                    <span className="full-screen-loader-eyebrow">Loading</span>
                </div>
                <p className="full-screen-loader-copy">
                    <strong>{label}</strong>
                    <span>{detail}</span>
                </p>
                <LoadingRule />
            </div>
        </div>
    );
}

export function WorkspaceRouteLoader({ label = "正在打开页面" }: { label?: string }) {
    const [visible, setVisible] = useState(false);

    useEffect(() => {
        const timer = window.setTimeout(() => setVisible(true), 140);
        return () => window.clearTimeout(timer);
    }, []);

    return (
        <section data-workspace-route-loader className={cn("workspace-route-loader", visible && "is-visible")} role="status" aria-live="polite" aria-label={label}>
            <div className="workspace-route-loader-content">
                <span className="workspace-route-loader-mark">
                    <BrandLogoFrame className="workspace-route-loader-logo" logoClassName="size-4" alt="" fallback={<span className="full-screen-loader-mark-fallback" />} />
                </span>
                <span className="workspace-route-loader-label">{label}</span>
            </div>
            <LoadingRule />
        </section>
    );
}

function LoadingRule() {
    return (
        <span className="loading-rule" aria-hidden="true">
            <i />
        </span>
    );
}
