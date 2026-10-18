import { animate } from "motion";

/**
 * 命令式 DOM 动效驱动：全站唯一入口，取代此前认证页单独引入的 anime.js。
 *
 * 三个必须保持的语义（换驱动时不能改）：
 * - 时长以毫秒传入。调用方沿用的是 CSS token 的毫秒口径，motion 内部用秒，这里换算。
 * - 缓动按名字给，内部换算成 Penner 方程函数；同名曲线的逐帧进度与 anime.js 一致。
 * - 结束或取消后把内联样式还原到动画前，不在元素上留下 transform / opacity 残值。
 */
export type DomMotionEase = "outCubic" | "inOutSine";

export type DomMotionKeyframes = Record<string, number | readonly number[]>;

export type DomMotionDelay = number | ((target: Element, index: number, targets: Element[]) => number);

export type DomMotionOptions = {
    /** 毫秒。 */
    duration: number;
    delay?: DomMotionDelay;
    ease?: DomMotionEase;
    onComplete?: () => void;
};

export type DomMotionHandle = {
    revert: () => void;
};

export type DomMotionDriver = (targets: Element | Element[], keyframes: DomMotionKeyframes, options: DomMotionOptions) => DomMotionHandle;

// anime.js 的 outCubic / inOutSine 就是下面两条 Penner 方程；用函数而不是 cubic-bezier
// 近似值传给 motion，才能保证换库后每一帧的进度值完全不变。
const EASINGS: Record<DomMotionEase, (progress: number) => number> = {
    outCubic: (progress) => 1 - (1 - progress) ** 3,
    inOutSine: (progress) => -(Math.cos(Math.PI * progress) - 1) / 2,
};

// 只记录本驱动可能写入的内联样式；其余属性交给调用方。
const MANAGED_STYLE_PROPERTIES = ["transform", "opacity", "will-change"] as const;

/**
 * 同一个元素上后来的动画会顶掉前一个，而前一个的延迟还原不能误伤后来者。
 * 每次开启动画给元素记一个代号，延迟还原只在自己的代号仍然有效时执行。
 */
const animationEpochs = new WeakMap<Element, number>();

function openEpoch(element: Element) {
    const epoch = (animationEpochs.get(element) ?? 0) + 1;
    animationEpochs.set(element, epoch);
    return epoch;
}

function rememberInlineStyles(element: Element): () => void {
    const style = (element as HTMLElement).style;
    if (!style) return () => {};
    const previous = MANAGED_STYLE_PROPERTIES.map((property) => [property, style.getPropertyValue(property), style.getPropertyPriority(property)] as const);
    return () => {
        for (const [property, value, priority] of previous) {
            if (value) style.setProperty(property, value, priority);
            else style.removeProperty(property);
        }
    };
}

export function animateDom(targets: Element | Element[], keyframes: DomMotionKeyframes, options: DomMotionOptions): DomMotionHandle {
    const list = (Array.isArray(targets) ? targets : [targets]).filter((target): target is Element => Boolean(target));
    const controls: { stop: () => void }[] = [];
    const cleanups: { restore: () => void; epoch: number; target: Element }[] = [];
    let finished = 0;
    let reverted = false;
    const duration = Math.max(0, options.duration) / 1000;
    const ease = EASINGS[options.ease ?? "outCubic"];

    // 逐个元素派发：motion 的 delay 只收数字，anime.js 的 stagger 语义由调用方给的函数还原。
    list.forEach((target, index) => {
        const epoch = openEpoch(target);
        const restore = rememberInlineStyles(target);
        cleanups.push({ restore, epoch, target });
        const delayMs = typeof options.delay === "function" ? options.delay(target, index, list) : (options.delay ?? 0);
        const control = animate(target, keyframes as never, {
            duration,
            delay: Math.max(0, delayMs) / 1000,
            ease,
            onComplete: () => {
                restore();
                finished += 1;
                if (finished < list.length || reverted) return;
                reverted = true;
                options.onComplete?.();
            },
        });
        controls.push(control as unknown as { stop: () => void });
    });

    if (!list.length) options.onComplete?.();

    return {
        revert() {
            if (reverted) return;
            reverted = true;
            for (const control of controls) control.stop();
            // stop() 只停在当前值，而且 motion 还有一帧排队写入会盖掉同步还原，
            // 因此同步清一次之后再跨两帧补清；代号变了的元素说明已经有新动画接手，跳过。
            const restoreAll = () => {
                for (const { restore, epoch, target } of cleanups) {
                    if (animationEpochs.get(target) !== epoch) continue;
                    restore();
                }
            };
            restoreAll();
            if (typeof requestAnimationFrame !== "function") return;
            requestAnimationFrame(() => {
                restoreAll();
                requestAnimationFrame(restoreAll);
            });
        },
    };
}
