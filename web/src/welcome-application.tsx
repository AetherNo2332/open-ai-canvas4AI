import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { getWelcomeAvailability } from "@/services/api/welcome";
import WelcomePage from "@/pages/welcome";
import "./styles/globals.css";
import { bootstrapAppearance } from "@/services/appearance-bootstrap";
import { applySkinTheme } from "@/lib/skin-themes";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import { useThemeStore } from "@/stores/use-theme-store";

async function renderWelcome() {
    try {
        await bootstrapAppearance();
        const mode = useThemeStore.getState().theme;
        document.documentElement.classList.toggle("dark", mode === "dark");
        applySkinTheme(useAppearanceStore.getState().appearance.activeSkin, mode);
        const { welcomeEnabled } = await getWelcomeAvailability();
        if (welcomeEnabled !== true) {
            window.location.replace("/");
            return;
        }
        createRoot(document.getElementById("root")!).render(<StrictMode><WelcomePage /></StrictMode>);
    } catch (error) {
        console.error("Welcome page initialization failed", error);
        createRoot(document.getElementById("root")!).render(
            <main role="alert">
                <p>暂时无法打开欢迎页，请稍后重试。</p>
                <button onClick={() => window.location.reload()}>重试</button>
                <a href="/">返回首页</a>
            </main>,
        );
    }
}

void renderWelcome();
