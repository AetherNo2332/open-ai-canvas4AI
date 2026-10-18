import { useCallback, useLayoutEffect, useRef } from "react";
import { useOutletContext } from "react-router";
import { createAuthMotionPlayer } from "./auth-motion";

type AuthMotionContext = { onValidationError: () => void };
const noop = () => {};

export function useAuthValidationFeedback() {
    return useOutletContext<AuthMotionContext | undefined>()?.onValidationError ?? noop;
}

export function useAuthMotion(mode: string) {
    const motionRoot = useRef<HTMLDivElement>(null);
    const feedback = useRef(noop);
    const onValidationError = useCallback(() => feedback.current(), []);

    useLayoutEffect(() => {
        const root = motionRoot.current;
        if (!root) return;
        const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
        const styles = getComputedStyle(root);
        const duration = (token: string, fallback: number) => {
            const value = styles.getPropertyValue(token).trim();
            const parsed = parseFloat(value);
            return Number.isFinite(parsed) ? Math.max(0, parsed * (value.endsWith("ms") ? 1 : 1000)) : fallback;
        };
        const player = createAuthMotionPlayer({ reducedMotion: () => reduced.matches, normalDuration: duration("--motion-state", 150), fastDuration: duration("--motion-instant", 120) });
        const entered = new WeakSet<HTMLElement>();
        let initial = true;
        const enter = () => {
            if (!root.querySelector("form")) return;
            // Animate shells once, never both an Ant input wrapper and its inner input.
            const candidates = Array.from(root.querySelectorAll<HTMLElement>(".ant-input-affix-wrapper, input.ant-input, .ant-btn, .auth-code-field, .auth-login-options, .auth-agreement-row"));
            const controls = candidates.filter((element) => !entered.has(element) && !candidates.some((parent) => parent !== element && parent.contains(element)));
            controls.forEach((element) => entered.add(element));
            player.enter(root, controls, initial, mode === "login" ? -1 : 1);
            initial = false;
        };
        feedback.current = () => {
            const form = root.querySelector("form");
            if (form) player.invalid(form);
        };
        const invalid = () => feedback.current();
        const press = (event: Event) => {
            if (event instanceof KeyboardEvent && (event.repeat || !["Enter", " "].includes(event.key))) return;
            if (event instanceof PointerEvent && event.button !== 0) return;
            const target = event.target instanceof Element ? event.target.closest<HTMLElement>(".ant-btn") : null;
            if (target && root.contains(target) && !target.matches(":disabled, [aria-disabled='true']")) player.press(target);
        };
        const preferenceChanged = () => {
            if (reduced.matches) player.dispose();
        };
        const observer = new MutationObserver(enter);
        observer.observe(root, { childList: true, subtree: true });
        root.addEventListener("invalid", invalid, true);
        root.addEventListener("pointerdown", press);
        root.addEventListener("keydown", press);
        reduced.addEventListener("change", preferenceChanged);
        enter();
        return () => {
            feedback.current = noop;
            observer.disconnect();
            root.removeEventListener("invalid", invalid, true);
            root.removeEventListener("pointerdown", press);
            root.removeEventListener("keydown", press);
            reduced.removeEventListener("change", preferenceChanged);
            player.dispose();
        };
    }, [mode]);

    return { motionRoot, onValidationError };
}
