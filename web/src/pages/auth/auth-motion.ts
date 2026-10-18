import { animateDom, type DomMotionDriver, type DomMotionHandle, type DomMotionKeyframes } from "@/lib/dom-motion";

type Animation = DomMotionHandle;
type MotionOptions = {
    reducedMotion: () => boolean;
    normalDuration: number;
    fastDuration: number;
    driver?: DomMotionDriver;
};

export function createAuthMotionPlayer(options: MotionOptions) {
    const driver = options.driver ?? animateDom;
    const active = new Set<Animation>();
    let press: Animation | undefined;
    let shake: Animation | undefined;
    let lastInvalid = -Infinity;
    const enabled = () => !options.reducedMotion() && options.normalDuration > 0;
    const play = (targets: HTMLElement | HTMLElement[], keyframes: DomMotionKeyframes, parameters: { duration: number; delay?: number | ((target: Element, index: number, targets: Element[]) => number); ease: "outCubic" | "inOutSine" }) => {
        const animation = driver(targets, keyframes, {
            ...parameters,
            onComplete: () => {
                animation.revert();
                active.delete(animation);
            },
        });
        active.add(animation);
        return animation;
    };
    const stop = (animation?: Animation) => {
        animation?.revert();
        if (animation) active.delete(animation);
    };
    return {
        enter(root: HTMLElement, controls: HTMLElement[], initial = true, direction = 1) {
            if (!enabled()) return;
            if (initial) play(root, { opacity: [0, 1], x: [14 * direction, 0] }, { duration: options.normalDuration * 2, ease: "outCubic" });
            if (controls.length) {
                const stagger = Math.min(55, options.normalDuration * 0.3);
                play(controls, { opacity: [0, 1], y: [8, 0] }, { duration: options.normalDuration * 1.5, delay: (_target, index) => index * stagger, ease: "outCubic" });
            }
        },
        press(button: HTMLElement) {
            if (!enabled() || options.fastDuration <= 0) return;
            stop(press);
            press = play(button, { scale: [1, 0.98, 1] }, { duration: options.fastDuration * 2, ease: "inOutSine" });
        },
        invalid(form: HTMLElement, now = Date.now()) {
            if (!enabled() || now - lastInvalid < 350) return;
            lastInvalid = now;
            stop(shake);
            shake = play(form, { x: [0, -5, 4, -3, 2, 0] }, { duration: options.normalDuration * 2, ease: "inOutSine" });
        },
        dispose() {
            active.forEach((animation) => animation.revert());
            active.clear();
        },
    };
}
