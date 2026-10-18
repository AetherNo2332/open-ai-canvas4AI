import { useEffect, useRef } from "react";
import { animateDom, type DomMotionHandle } from "@/lib/dom-motion";
import { insertedAuthText, permitsAuthTyping } from "./auth-typing";
import { createAuthButtonHalo } from "./auth-button-halo";

export function useAuthAmbientMotion(theme: string) {
    const sceneRoot = useRef<HTMLElement>(null);

    useEffect(() => {
        const root = sceneRoot.current;
        if (!root) return;
        const reduced = matchMedia("(prefers-reduced-motion: reduce)");
        const pointer = matchMedia("(hover: hover) and (pointer: fine)");
        const durationToken = getComputedStyle(root).getPropertyValue("--motion-state").trim();
        const parsed = parseFloat(durationToken);
        const duration = Number.isFinite(parsed) ? Math.max(0, parsed * (durationToken.endsWith("ms") ? 1 : 1000)) : 150;
        const halo = createAuthButtonHalo();
        let frame = 0;
        let pointerX = 0;
        let pointerY = 0;
        let hoveredButton: HTMLElement | null = null;
        const move = (event: PointerEvent) => {
            if (reduced.matches || !pointer.matches || event.pointerType === "touch" || duration === 0) return hide();
            const button = event.target instanceof Element ? event.target.closest<HTMLElement>(".ant-btn, .auth-theme-toggle") : null;
            if (!button || !root.contains(button) || button.matches(":disabled, [aria-disabled='true'], .ant-btn-disabled")) return hide();
            hoveredButton = button;
            pointerX = event.clientX;
            pointerY = event.clientY;
            if (frame) return;
            frame = requestAnimationFrame(() => {
                frame = 0;
                halo.move(hoveredButton, pointerX, pointerY);
            });
        };
        const hide = () => {
            cancelAnimationFrame(frame);
            frame = 0;
            hoveredButton = null;
            halo.hide();
        };

        const previous = new WeakMap<HTMLInputElement, string>();
        let composing: HTMLInputElement | null = null;
        let clearTyping = () => {};
        const textInput = (event: Event) => {
            const target = event.target;
            return target instanceof HTMLInputElement && root.contains(target) && permitsAuthTyping(target.type, target.autocomplete, Boolean(target.closest(".ant-input-password"))) ? target : null;
        };
        const remember = (event: Event) => {
            const input = textInput(event);
            if (input && input !== composing) previous.set(input, input.value);
        };
        const showTyping = (input: HTMLInputElement) => {
            const oldValue = previous.get(input) ?? input.value;
            if (oldValue === input.value) return; // Some IMEs emit a duplicate input event after compositionend.
            const change = insertedAuthText(oldValue, input.value);
            previous.set(input, input.value);
            clearTyping();
            if (!change || reduced.matches || duration === 0 || document.activeElement !== input) return;
            // Keep the real input, caret, selection and IME untouched. Only paint a short-lived visual copy.
            const styles = getComputedStyle(input);
            const rect = input.getBoundingClientRect();
            const layer = document.createElement("div");
            layer.className = "auth-typing-layer";
            layer.setAttribute("aria-hidden", "true");
            Object.assign(layer.style, {
                left: `${rect.left + parseFloat(styles.borderLeftWidth) + parseFloat(styles.paddingLeft)}px`,
                top: `${rect.top + parseFloat(styles.borderTopWidth) + parseFloat(styles.paddingTop)}px`,
                width: `${input.clientWidth - parseFloat(styles.paddingLeft) - parseFloat(styles.paddingRight)}px`,
                height: `${input.clientHeight - parseFloat(styles.paddingTop) - parseFloat(styles.paddingBottom)}px`,
                font: styles.font,
                letterSpacing: styles.letterSpacing,
                lineHeight: styles.lineHeight,
                color: styles.color,
                direction: styles.direction,
                textAlign: styles.textAlign,
            });
            const text = document.createElement("span");
            text.style.transform = `translateX(${-input.scrollLeft}px)`;
            const fresh = document.createElement("span");
            fresh.className = "auth-typing-inserted";
            fresh.textContent = change.inserted;
            text.append(document.createTextNode(change.before), fresh, document.createTextNode(change.after));
            layer.append(text);
            root.append(layer);
            const fill = input.style.getPropertyValue("-webkit-text-fill-color");
            const priority = input.style.getPropertyPriority("-webkit-text-fill-color");
            input.style.setProperty("-webkit-text-fill-color", "transparent", "important");
            let animation: DomMotionHandle | undefined;
            clearTyping = () => {
                animation?.revert();
                layer.remove();
                if (fill) input.style.setProperty("-webkit-text-fill-color", fill, priority);
                else input.style.removeProperty("-webkit-text-fill-color");
                clearTyping = () => {};
            };
            animation = animateDom(fresh, { opacity: [0.35, 1], y: [2, 0] }, { duration, ease: "outCubic", onComplete: () => clearTyping() });
        };
        const inputChanged = (event: Event) => {
            const input = textInput(event);
            if (!input || composing === input || (event instanceof InputEvent && event.isComposing)) return;
            showTyping(input);
        };
        const compositionStart = (event: Event) => {
            clearTyping();
            composing = textInput(event);
            if (composing) previous.set(composing, composing.value);
        };
        const compositionEnd = () => {
            const input = composing;
            composing = null;
            if (input) showTyping(input);
        };
        const clear = () => {
            hide();
            clearTyping();
        };
        const preferenceChanged = () => {
            hide();
            clear();
        };
        root.addEventListener("pointermove", move);
        root.addEventListener("pointerleave", hide);
        root.addEventListener("beforeinput", remember);
        root.addEventListener("focusin", remember);
        root.addEventListener("input", inputChanged);
        root.addEventListener("compositionstart", compositionStart);
        root.addEventListener("compositionend", compositionEnd);
        root.addEventListener("focusout", clear);
        root.addEventListener("scroll", clear, true);
        window.addEventListener("resize", clear);
        window.addEventListener("blur", preferenceChanged);
        reduced.addEventListener("change", preferenceChanged);
        pointer.addEventListener("change", preferenceChanged);
        return () => {
            hide();
            clear();
            root.removeEventListener("pointermove", move);
            root.removeEventListener("pointerleave", hide);
            root.removeEventListener("beforeinput", remember);
            root.removeEventListener("focusin", remember);
            root.removeEventListener("input", inputChanged);
            root.removeEventListener("compositionstart", compositionStart);
            root.removeEventListener("compositionend", compositionEnd);
            root.removeEventListener("focusout", clear);
            root.removeEventListener("scroll", clear, true);
            window.removeEventListener("resize", clear);
            window.removeEventListener("blur", preferenceChanged);
            reduced.removeEventListener("change", preferenceChanged);
            pointer.removeEventListener("change", preferenceChanged);
        };
    }, [theme]);
    return { sceneRoot };
}
