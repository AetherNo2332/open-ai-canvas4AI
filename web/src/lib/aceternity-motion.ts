/**
 * 动效配置（对应 #99 动效系统）
 *
 * 这里的 duration 是 JS 多段入场动效（入场分展、光斑、面板弹入）自己的时长，单位秒。
 * 它与 CSS 的 `--motion-*` **不是同一套值**，不要按下面的近似关系替换：
 *   instant 0.12s  对 CSS 的瞬时反馈（--motion-instant，皮肤默认 150ms）
 *   state   0.20s  对 CSS 的常规过渡（--motion-state，皮肤默认 150ms）
 *   panel   0.32s  对 CSS 的面板过渡（--motion-panel，皮肤默认 150ms）
 * 多段入场需要比单次状态切换更长的可见时间；直接把 JS 时长换成 CSS token 会把
 * 首页入场从 200/320ms 缩短到 150ms，属于可见的观感变化，因此两边各自独立维护。
 *
 * easing 与 CSS token 的对应是可以对齐的：
 *   enter  [0.2, 0.85, 0.18, 1] ≈ --motion-ease-out
 *   exit   [0.4, 0, 1, 1]       ≈ --motion-ease-in
 *
 * prefers-reduced-motion 由组件层 useReducedMotion() 控制。
 */
export const aceternityMotion = {
    duration: {
        instant: 0.12,
        state: 0.2,
        panel: 0.32,
    },
    spring: {
        dock: { mass: 0.12, stiffness: 220, damping: 18 },
        surface: { mass: 0.32, stiffness: 280, damping: 26 },
        panel: { mass: 0.42, stiffness: 320, damping: 28 },
    },
    easing: {
        enter: [0.2, 0.85, 0.18, 1] as const,
        exit: [0.4, 0, 1, 1] as const,
    },
} as const;
