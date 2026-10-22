import { AnimatePresence, motion, MotionConfig, useReducedMotion } from "motion/react";
import { lazy, Suspense, useEffect, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";

import { BannerAnnouncementsSlider } from "@/components/layout/banner-announcements-slider";
import { ModelSetupGuide } from "@/components/layout/model-setup-guide";
import { WorkspaceSidebarNav } from "@/components/layout/workspace-sidebar-nav";
import { readWorkspaceSidebarCollapsed, writeWorkspaceSidebarCollapsed } from "@/components/layout/workspace-sidebar-state";
import { WorkspaceTopBar } from "@/components/layout/workspace-top-bar";
import { WorkspaceTopBarExtensionProvider } from "@/components/layout/workspace-top-bar-extension";
import { WorkspaceWalletHost } from "@/components/layout/workspace-wallet-modal";
import { cn } from "@/lib/utils";
import { isSpatialWorkbenchPath } from "@/lib/workspace-routes";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import "./workspace-sidebar-motion.css";

const mobileQuery = "(max-width: 1023px)";
const subscribeViewport = (callback: () => void) => {
    const query = window.matchMedia(mobileQuery);
    query.addEventListener("change", callback);
    return () => query.removeEventListener("change", callback);
};
const getMobileViewport = () => window.matchMedia(mobileQuery).matches;

const WorkspaceCommandPalette = lazy(() => import("@/components/layout/workspace-command-palette").then((module) => ({ default: module.WorkspaceCommandPalette })));

export function AppWorkspaceShell({ children }: { children: ReactNode }) {
    const { pathname } = useLocation();
    const navigate = useNavigate();
    const [mobileSidebarExpanded, setMobileSidebarExpanded] = useState(false);
    const [desktopSidebarCollapsed, setDesktopSidebarCollapsed] = useState(readWorkspaceSidebarCollapsed);
    const [paletteOpen, setPaletteOpen] = useState(false);
    const mobileViewport = useSyncExternalStore(subscribeViewport, getMobileViewport, () => false);
    const reducedMotion = useReducedMotion();
    const motionNormal = useAppearanceStore((state) => state.appearance.activeSkin.tokens.components.motionNormal);
    const sidebarRef = useRef<HTMLElement>(null);
    const sidebarCollapsed = !mobileViewport && desktopSidebarCollapsed;
    const sidebarHidden = mobileViewport && !mobileSidebarExpanded;
    const sidebarTransition = { duration: reducedMotion ? 0 : motionNormal / 1000, ease: [0.2, 0.8, 0.2, 1] as const };
    const closeMobileSidebar = () => {
        if (sidebarRef.current?.contains(document.activeElement)) {
            document.querySelector<HTMLButtonElement>(".app-workspace-mobile-menu")?.focus();
        }
        setMobileSidebarExpanded(false);
    };

    useEffect(() => {
        if (!mobileViewport || !mobileSidebarExpanded) return;
        const handleEscape = (event: KeyboardEvent) => {
            if (event.key === "Escape") closeMobileSidebar();
        };
        window.addEventListener("keydown", handleEscape);
        return () => window.removeEventListener("keydown", handleEscape);
    }, [mobileViewport, mobileSidebarExpanded]);

    const hideChrome = pathname.startsWith("/admin") || /^\/canvas\/[^/]+/.test(pathname);
    const showGlobalTopBar = !hideChrome;
    const spatialWorkbench = isSpatialWorkbenchPath(pathname);
    const creationWorkspace = pathname === "/" || pathname === "/create";

    const toggleSidebar = () => {
        if (mobileViewport) {
            setMobileSidebarExpanded((current) => !current);
            return;
        }
        setDesktopSidebarCollapsed((current) => {
            const next = !current;
            writeWorkspaceSidebarCollapsed(next);
            return next;
        });
    };

    const expandDesktopSidebar = () => {
        setDesktopSidebarCollapsed(false);
        writeWorkspaceSidebarCollapsed(false);
    };

    const collapseDesktopSidebar = () => {
        setDesktopSidebarCollapsed(true);
        writeWorkspaceSidebarCollapsed(true);
    };

    const handleNavClick = () => {
        if (mobileViewport) closeMobileSidebar();
    };

    // ⌘K / Ctrl+K 全局呼出搜索面板。
    useEffect(() => {
        const handler = (event: KeyboardEvent) => {
            if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
                event.preventDefault();
                setPaletteOpen((open) => !open);
            }
        };
        window.addEventListener("keydown", handler);
        return () => window.removeEventListener("keydown", handler);
    }, []);

    useEffect(() => {
        const handleWorkspaceNavigation = (rawEvent: Event) => {
            const event = rawEvent as CustomEvent<{ to?: string }>;
            if (!event.detail?.to) return;
            event.preventDefault();
            navigate(event.detail.to);
        };
        window.addEventListener("workspace:navigate", handleWorkspaceNavigation);
        return () => window.removeEventListener("workspace:navigate", handleWorkspaceNavigation);
    }, [navigate]);

    return (
        <>
            <WorkspaceTopBarExtensionProvider>
                <div className={cn("app-workspace-shell flex h-dvh min-h-0 w-full flex-col overflow-hidden", spatialWorkbench && "is-spatial", creationWorkspace && "is-creation-workspace")}>
                    <AnimatePresence initial={false}>
                        {!hideChrome && mobileViewport && mobileSidebarExpanded ? (
                            <motion.button
                                key="sidebar-scrim"
                                type="button"
                                className="app-workspace-sidebar-scrim lg:hidden"
                                aria-label="收起侧栏"
                                initial={{ opacity: 0 }}
                                animate={{ opacity: 1 }}
                                exit={{ opacity: 0 }}
                                transition={sidebarTransition}
                                onClick={closeMobileSidebar}
                            />
                        ) : null}
                    </AnimatePresence>

                    {showGlobalTopBar ? <BannerAnnouncementsSlider /> : null}

                    <div className="app-workspace-main-row flex min-h-0 min-w-0 flex-1 overflow-hidden">
                        {!hideChrome ? (
                            <motion.aside
                                ref={sidebarRef}
                                id="workspace-sidebar"
                                data-motion-sidebar
                                aria-label="工作台导航"
                                aria-hidden={sidebarHidden || undefined}
                                inert={sidebarHidden}
                                initial={false}
                                animate={{ "--sidebar-expansion": sidebarCollapsed ? 0 : 1, x: sidebarHidden ? "-100%" : "0%" }}
                                transition={sidebarTransition}
                                style={{ "--sidebar-expansion": sidebarCollapsed ? 0 : 1 } as CSSProperties}
                                className={cn("app-workspace-sidebar flex h-full shrink-0 flex-col overflow-hidden", mobileSidebarExpanded && "is-mobile-expanded", sidebarCollapsed && "is-collapsed")}
                            >
                                <MotionConfig reducedMotion="user" transition={sidebarTransition}>
                                    <WorkspaceSidebarNav
                                        collapsed={sidebarCollapsed}
                                        onNavigate={handleNavClick}
                                        onOpenSearch={() => setPaletteOpen(true)}
                                        onExpand={expandDesktopSidebar}
                                        onCollapse={mobileViewport ? closeMobileSidebar : collapseDesktopSidebar}
                                    />
                                </MotionConfig>
                            </motion.aside>
                        ) : null}

                        <div className="app-workspace-stage relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
                            {showGlobalTopBar ? <WorkspaceTopBar sidebarOpen={mobileViewport ? mobileSidebarExpanded : !desktopSidebarCollapsed} onToggleSidebar={toggleSidebar} /> : null}
                            <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">{children}</div>
                        </div>
                    </div>

                    {paletteOpen ? (
                        <Suspense fallback={null}>
                            <WorkspaceCommandPalette open onClose={() => setPaletteOpen(false)} />
                        </Suspense>
                    ) : null}
                </div>
            </WorkspaceTopBarExtensionProvider>
            <WorkspaceWalletHost />
            <ModelSetupGuide hidden={pathname === "/login" || pathname === "/register" || pathname.startsWith("/admin")} />
        </>
    );
}
