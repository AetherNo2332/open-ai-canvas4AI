import { useEffect } from "react";

/** Arm closing motion on actual activation, avoiding animation on page load. */
export function useSkinSwitchMotion() {
    useEffect(() => {
        const pending = new Map<HTMLElement, number>();
        const activate = (event: MouseEvent) => {
            const control = event.target instanceof Element ? event.target.closest<HTMLElement>(".ant-switch") : null;
            if (!control || control.matches(":disabled, [aria-disabled='true']") || control.getAttribute("aria-checked") !== "true") return;
            const previous = pending.get(control);
            if (previous !== undefined) window.clearTimeout(previous);
            control.dataset.skinSwitchClosing = "true";
            const duration = getComputedStyle(control).getPropertyValue("--skin-motion-state").trim();
            const milliseconds = parseFloat(duration) * (duration.endsWith("ms") ? 1 : 1000);
            pending.set(
                control,
                window.setTimeout(
                    () => {
                        delete control.dataset.skinSwitchClosing;
                        pending.delete(control);
                    },
                    (Number.isFinite(milliseconds) ? milliseconds : 150) + 50,
                ),
            );
        };
        document.addEventListener("click", activate, true);
        return () => {
            document.removeEventListener("click", activate, true);
            for (const [control, timer] of pending) {
                window.clearTimeout(timer);
                delete control.dataset.skinSwitchClosing;
            }
        };
    }, []);
}
