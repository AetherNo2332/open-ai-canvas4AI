import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronUp, ChevronDown, Camera as CameraIcon, Aperture as ApertureIcon, RotateCcw, X } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";

import { SpotlightSurface } from "@/components/ui/aceternity/spotlight-surface";
import { Tooltip } from "@/components/ui/base/tooltip";
import { Switch } from "@/components/ui/base/switch";
import { aceternityMotion } from "@/lib/aceternity-motion";
import { canvasThemes } from "@/lib/canvas-theme";
import { useCopyText } from "@/hooks/use-copy-text";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";
import {
    APERTURES,
    APERTURE_META,
    CAMERA_PROFILES,
    FOCAL_LENGTHS,
    FOCAL_LENGTH_META,
    LENS_PROFILES,
    buildCameraPrompt,
    cameraProfileIndex,
    lensProfileIndex,
    type CameraControlOptions,
} from "@/lib/canvas/camera-prompt-library";
import { CAMERA_WHEEL_IDLE_RESET_MS, normalizeCameraWheelDelta, takeCameraWheelStep } from "@/lib/canvas/camera-wheel";

const defaultCameraControl: CameraControlOptions = {
    enabled: false,
    camera: "arri_alexa_65",
    lens: "arri_signature_prime",
    focalLength: 50,
    aperture: 2.8,
};

/* ── 产品图 ── */

/** 相机/镜头卡片直接展示真实设备产品图（内容图片，保留原色）；加载失败时退回图标，避免留下空白。 */
function EquipmentImage({ src, className, fallback }: { src: string; className: string; fallback: React.ReactNode }) {
    const [failed, setFailed] = useState(false);
    useEffect(() => setFailed(false), [src]);
    if (failed) return <span className="grid h-full w-full place-items-center opacity-60">{fallback}</span>;
    return <img src={src} alt="" className={className} draggable={false} loading="lazy" decoding="async" onError={() => setFailed(true)} />;
}

/* ── HoverTip ── */

interface HoverTipProps {
    title: string;
    description?: string;
    useCase?: string;
    children: React.ReactNode;
}

const HoverTip = memo(({ title, description, useCase, children }: HoverTipProps) => {
    const theme = canvasThemes[useActiveTheme()];
    const [coords, setCoords] = useState<{ left: number; top: number; placement: "top" | "bottom" } | null>(null);
    const anchorRef = useRef<HTMLDivElement | null>(null);
    const tipRef = useRef<HTMLDivElement | null>(null);
    const timerRef = useRef<number | null>(null);

    const computePlacement = useCallback(() => {
        const el = anchorRef.current;
        if (!el) return null;
        const rect = el.getBoundingClientRect();
        const tooltipWidth = 260;
        const margin = 10;
        let left = rect.left + rect.width / 2 - tooltipWidth / 2;
        left = Math.max(margin, Math.min(window.innerWidth - tooltipWidth - margin, left));
        const placement: "top" | "bottom" = "top";
        const top = rect.top - margin;
        return { left, top, placement };
    }, []);

    useEffect(() => {
        if (!coords) return;
        const tip = tipRef.current;
        const anchor = anchorRef.current;
        if (!tip || !anchor) return;
        const tipH = tip.getBoundingClientRect().height;
        const rect = anchor.getBoundingClientRect();
        const margin = 10;
        let placement = coords.placement;
        if (placement === "top" && rect.top - tipH - margin < 0) placement = "bottom";
        if (placement !== coords.placement) {
            const top = placement === "top" ? rect.top - margin : rect.bottom + margin;
            setCoords((c) => (c ? { ...c, placement, top } : c));
        }
    }, [coords]);

    const handleEnter = () => {
        if (timerRef.current) clearTimeout(timerRef.current);
        timerRef.current = window.setTimeout(() => {
            const c = computePlacement();
            if (c) setCoords(c);
        }, 900);
    };
    const handleLeave = () => {
        if (timerRef.current) clearTimeout(timerRef.current);
        timerRef.current = null;
        setCoords(null);
    };

    // 纵向 flex 的 align-items:center 会让包裹层收缩到内容宽度，卡片 w-full 随之失效；
    // 这里显式占满整列，否则角标会盖住居中的焦距/光圈标签，四张卡宽度也不一致。
    return (
        <div ref={anchorRef} className="relative w-full" onMouseEnter={handleEnter} onMouseLeave={handleLeave}>
            {children}
            {coords &&
                createPortal(
                    <div
                        ref={tipRef}
                        className="pointer-events-none fixed z-[var(--z-max)] w-[260px] rounded-[var(--overlay-radius)] border p-3 shadow-2xl"
                        style={{
                            left: `${coords.left}px`,
                            top: coords.placement === "bottom" ? `${coords.top}px` : undefined,
                            bottom: coords.placement === "top" ? `${window.innerHeight - coords.top}px` : undefined,
                            borderColor: theme.toolbar.border,
                            background: theme.spatial.elevated,
                            color: theme.node.text,
                        }}
                    >
                        <div className="text-[var(--fs-caption)] font-semibold">{title}</div>
                        {description && <div className="mt-1 text-[var(--fs-label)] leading-5" style={{ color: theme.node.muted }}>{description}</div>}
                        {useCase && <div className="mt-2 rounded-[var(--menu-radius)] px-2 py-1 text-[var(--fs-label)]" style={{ background: theme.toolbar.itemHover, color: theme.node.muted }}>使用场景：{useCase}</div>}
                    </div>,
                    document.body,
                )}
        </div>
    );
});
HoverTip.displayName = "HoverTip";

/* ── 滚轮切档 ── */

/**
 * 参数卡上的滚轮只切换档位，不滚动弹层也不缩放画布；一次事件最多切一档。
 * 必须用非 passive 的原生监听，才能在滚轮事件里阻止默认滚动。
 */
function useCameraWheelStep(onStep: (direction: 1 | -1) => void) {
    const targetRef = useRef<HTMLDivElement | null>(null);
    const onStepRef = useRef(onStep);
    onStepRef.current = onStep;
    useEffect(() => {
        const target = targetRef.current;
        if (!target) return;
        let accumulator = 0;
        let resetTimer: number | null = null;
        const handleWheel = (event: WheelEvent) => {
            // 保留 Ctrl+滚轮的浏览器缩放行为。
            if (event.ctrlKey) return;
            const delta = normalizeCameraWheelDelta(event.deltaY, event.deltaMode);
            if (!delta) return;
            event.preventDefault();
            const next = takeCameraWheelStep(accumulator, delta);
            accumulator = next.accumulator;
            if (resetTimer !== null) window.clearTimeout(resetTimer);
            resetTimer = window.setTimeout(() => {
                accumulator = 0;
            }, CAMERA_WHEEL_IDLE_RESET_MS);
            if (next.step) onStepRef.current(next.step);
        };
        target.addEventListener("wheel", handleWheel, { passive: false });
        return () => {
            target.removeEventListener("wheel", handleWheel);
            if (resetTimer !== null) window.clearTimeout(resetTimer);
        };
    }, []);
    return targetRef;
}

/* ── CardColumn ── */

interface CardColumnProps {
    label: string;
    tooltipTitle: string;
    tooltipDesc?: string;
    tooltipUseCase?: string;
    visual: React.ReactNode;
    cornerBadge?: React.ReactNode;
    captionBelow: string;
    onPrev: () => void;
    onNext: () => void;
    onWheelStep: (direction: 1 | -1) => void;
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
}

const CardColumn = memo(({ label, tooltipTitle, tooltipDesc, tooltipUseCase, visual, cornerBadge, captionBelow, onPrev, onNext, onWheelStep, theme }: CardColumnProps) => {
    const wheelRef = useCameraWheelStep(onWheelStep);
    return (
        <div ref={wheelRef} data-camera-column={label} data-canvas-wheel-scroll className="flex min-w-0 flex-col items-center gap-2">
            <button
                type="button"
                onClick={onPrev}
                className="flex h-5 w-full items-center justify-center rounded-[var(--button-radius)] transition-colors"
                style={{ color: theme.node.faint }}
                title="上一项"
            >
                <ChevronUp className="size-3.5" />
            </button>
            <HoverTip title={tooltipTitle} description={tooltipDesc} useCase={tooltipUseCase}>
                <div
                    className="relative flex h-[clamp(116px,14vw,140px)] w-full min-w-0 cursor-help flex-col items-center justify-between rounded-[var(--card-radius)] border px-2 pt-2 pb-2 transition-colors"
                    style={{ borderColor: theme.toolbar.border, background: theme.toolbar.itemHover }}
                >
                    <span className="text-[var(--fs-label)] font-medium tracking-wide" style={{ color: theme.node.muted }}>{label}</span>
                    <div className="flex w-full flex-1 items-center justify-center">{visual}</div>
                    {cornerBadge && (
                        <div className="absolute right-1.5 top-1.5 rounded-[var(--menu-radius)] border px-2 py-0.5 text-[var(--fs-label)] font-medium" style={{ background: theme.spatial.elevated, borderColor: theme.toolbar.border, color: theme.node.muted }}>
                            {cornerBadge}
                        </div>
                    )}
                </div>
            </HoverTip>
            <button
                type="button"
                onClick={onNext}
                className="flex h-5 w-full items-center justify-center rounded-[var(--button-radius)] transition-colors"
                style={{ color: theme.node.faint }}
                title="下一项"
            >
                <ChevronDown className="size-3.5" />
            </button>
            <span className="max-w-full truncate text-center text-[var(--fs-label)] leading-4" style={{ color: theme.node.muted }}>{captionBelow}</span>
        </div>
    );
});
CardColumn.displayName = "CardColumn";

/* ── Main panel ── */

export function CanvasNodeCameraPanel({
    cameraControl,
    onClose,
    onConfirm,
}: {
    cameraControl?: CameraControlOptions;
    onClose: () => void;
    onConfirm: (options: CameraControlOptions, prompt: string) => void;
}) {
    const theme = canvasThemes[useActiveTheme()];
    const reducedMotion = useReducedMotion();
    const copyText = useCopyText();

    const [enabled, setEnabled] = useState(cameraControl?.enabled ?? defaultCameraControl.enabled);
    // 新建草稿按声明的默认机身/镜头开始；已保存的枚举值可能已下线，退回首项展示但要求用户显式重新选择后再应用，避免静默改写配置。
    // 上游目录时期的 id 经别名映射后预选对应项，应用时写回现有 id。
    const initialCameraIdx = cameraProfileIndex(cameraControl?.camera ?? defaultCameraControl.camera);
    const initialLensIdx = lensProfileIndex(cameraControl?.lens ?? defaultCameraControl.lens);
    const [cameraIdx, setCameraIdx] = useState(initialCameraIdx >= 0 ? initialCameraIdx : 0);
    const [lensIdx, setLensIdx] = useState(initialLensIdx >= 0 ? initialLensIdx : 0);
    const [cameraInvalid, setCameraInvalid] = useState(Boolean(cameraControl) && initialCameraIdx < 0);
    const [lensInvalid, setLensInvalid] = useState(Boolean(cameraControl) && initialLensIdx < 0);
    const [focal, setFocal] = useState(cameraControl?.focalLength ?? defaultCameraControl.focalLength);
    const [aperture, setAperture] = useState(cameraControl?.aperture ?? defaultCameraControl.aperture);

    const currentCamera = CAMERA_PROFILES[cameraIdx];
    const currentLens = LENS_PROFILES[lensIdx];
    const focalMeta = FOCAL_LENGTH_META[focal];
    const apertureMeta = APERTURE_META[aperture];
    const invalidSavedConfig = cameraInvalid || lensInvalid;

    const summary = useMemo(() => `${currentCamera.zhName} · ${currentLens.zhName} · ${focal}mm · f/${aperture}`, [currentCamera, currentLens, focal, aperture]);

    const cycleCamera = (dir: 1 | -1) => {
        setCameraIdx((i) => (i + dir + CAMERA_PROFILES.length) % CAMERA_PROFILES.length);
        setCameraInvalid(false);
    };
    const cycleLens = (dir: 1 | -1) => {
        setLensIdx((i) => (i + dir + LENS_PROFILES.length) % LENS_PROFILES.length);
        setLensInvalid(false);
    };
    const cycleFocal = (dir: 1 | -1) => setFocal((f) => {
        const idx = FOCAL_LENGTHS.indexOf(f as (typeof FOCAL_LENGTHS)[number]);
        const nextIdx = (idx + dir + FOCAL_LENGTHS.length) % FOCAL_LENGTHS.length;
        return FOCAL_LENGTHS[nextIdx];
    });
    const cycleAperture = (dir: 1 | -1) => setAperture((a) => {
        const idx = APERTURES.indexOf(a as (typeof APERTURES)[number]);
        const nextIdx = (idx + dir + APERTURES.length) % APERTURES.length;
        return APERTURES[nextIdx];
    });

    const buildPrompt = useCallback(() => {
        if (!enabled) return "";
        return buildCameraPrompt({
            cameraId: currentCamera.id,
            lensId: currentLens.id,
            focalLengthMm: focal,
            apertureF: aperture,
        });
    }, [enabled, currentCamera, currentLens, focal, aperture]);

    const handleApply = () => {
        if (invalidSavedConfig) return;
        const options: CameraControlOptions = { enabled, camera: currentCamera.id, lens: currentLens.id, focalLength: focal, aperture };
        onConfirm(options, buildPrompt());
    };

    const handleReset = () => {
        setEnabled(defaultCameraControl.enabled);
        setCameraIdx(Math.max(0, CAMERA_PROFILES.findIndex((c) => c.id === defaultCameraControl.camera)));
        setLensIdx(Math.max(0, LENS_PROFILES.findIndex((l) => l.id === defaultCameraControl.lens)));
        setCameraInvalid(false);
        setLensInvalid(false);
        setFocal(defaultCameraControl.focalLength);
        setAperture(defaultCameraControl.aperture);
    };

    const secondaryButtonClass = "flex h-8 items-center gap-2 rounded-[var(--dock-item-radius)] px-3 text-[var(--fs-label)] font-medium transition-colors hover:bg-[color-mix(in_oklab,currentColor_10%,transparent)] disabled:cursor-not-allowed disabled:opacity-45";

    return (
        <SpotlightSurface
            data-canvas-no-zoom
            spotlightColor={theme.toolbar.itemHover}
            initial={reducedMotion ? { opacity: 0 } : { opacity: 0, y: 8, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            transition={reducedMotion ? { duration: 0 } : aceternityMotion.spring.panel}
            className="w-full overflow-hidden rounded-[var(--r-xl)] border backdrop-blur-2xl"
            style={{ background: theme.spatial.elevated, borderColor: theme.toolbar.border, color: theme.node.text, boxShadow: `0 28px 80px ${theme.spatial.shadow}` }}
            onMouseDown={(event) => event.stopPropagation()}
            onPointerDown={(event) => event.stopPropagation()}
        >
            <div className="flex items-center justify-between gap-3 border-b px-4 py-3" style={{ borderColor: theme.toolbar.border }}>
                <div className="min-w-0">
                    <div className="text-sm font-semibold tracking-tight">摄像机控制</div>
                    <div className="mt-0.5 truncate text-[var(--fs-label)]" style={{ color: theme.node.muted }}>为当前图片生成设置镜头、焦距与光圈，滚轮可切换档位</div>
                </div>
                <button
                    type="button"
                    className="grid size-7 shrink-0 place-items-center rounded-[var(--button-radius)] transition-colors hover:bg-[color-mix(in_oklab,currentColor_10%,transparent)]"
                    style={{ color: theme.node.muted }}
                    onClick={onClose}
                    aria-label="关闭摄像机控制"
                    title="关闭"
                >
                    <X className="size-4" />
                </button>
            </div>

            {/* 相机、镜头、焦距和光圈均来自已注册枚举，确认时由纯函数再次强校验。 */}
            <div className="grid grid-cols-2 gap-2 px-4 py-4 sm:grid-cols-4">
                <CardColumn
                    label="相机"
                    tooltipTitle={`${currentCamera.zhName} · ${currentCamera.label}`}
                    tooltipDesc={currentCamera.description}
                    tooltipUseCase={currentCamera.useCase}
                    visual={<EquipmentImage src={currentCamera.image} className="h-16 w-auto max-w-full object-contain" fallback={<CameraIcon className="size-8" />} />}
                    captionBelow={currentCamera.zhName}
                    onPrev={() => cycleCamera(-1)}
                    onNext={() => cycleCamera(1)}
                    onWheelStep={(direction) => cycleCamera(direction)}
                    theme={theme}
                />
                <CardColumn
                    label="镜头"
                    tooltipTitle={`${currentLens.zhName} · ${currentLens.label}`}
                    tooltipDesc={currentLens.description}
                    tooltipUseCase={currentLens.useCase}
                    visual={<EquipmentImage src={currentLens.image} className="h-14 w-auto max-w-full object-contain" fallback={<ApertureIcon className="size-7" />} />}
                    captionBelow={currentLens.zhName}
                    onPrev={() => cycleLens(-1)}
                    onNext={() => cycleLens(1)}
                    onWheelStep={(direction) => cycleLens(direction)}
                    theme={theme}
                />
                <CardColumn
                    label="焦距"
                    tooltipTitle={`${focal}mm · ${focalMeta?.zhName ?? ""}`}
                    tooltipDesc={focalMeta?.description}
                    tooltipUseCase={focalMeta?.useCase}
                    visual={
                        <div className="flex flex-col items-center">
                            <div className="text-[var(--fs-display)] font-normal leading-none">{focal}</div>
                            <div className="mt-1 text-[var(--fs-label)] tracking-wider" style={{ color: theme.node.faint }}>mm</div>
                        </div>
                    }
                    cornerBadge={focalMeta?.zhName}
                    captionBelow={focalMeta?.zhName ?? ""}
                    onPrev={() => cycleFocal(-1)}
                    onNext={() => cycleFocal(1)}
                    onWheelStep={(direction) => cycleFocal(direction)}
                    theme={theme}
                />
                <CardColumn
                    label="光圈"
                    tooltipTitle={`f/${aperture} · ${apertureMeta?.zhName ?? ""}`}
                    tooltipDesc={apertureMeta?.description}
                    tooltipUseCase={apertureMeta?.useCase}
                    visual={
                        <div className="flex flex-col items-center">
                            <div className="text-[var(--fs-display)] font-normal leading-none">
                                <span className="text-[var(--fs-heading)]" style={{ color: theme.node.faint }}>f/</span>{aperture}
                            </div>
                            <div className="mt-1 text-[var(--fs-label)] tracking-wider" style={{ color: theme.node.faint }}>aperture</div>
                        </div>
                    }
                    cornerBadge={apertureMeta?.zhName}
                    captionBelow={apertureMeta?.zhName ?? ""}
                    onPrev={() => cycleAperture(-1)}
                    onNext={() => cycleAperture(1)}
                    onWheelStep={(direction) => cycleAperture(direction)}
                    theme={theme}
                />
            </div>

            {/* 当前有效配置摘要；已保存配置不可用时要求显式重新选择。 */}
            <div className="mx-4 mb-3 rounded-[var(--card-radius)] border px-4 py-2" style={{ borderColor: theme.toolbar.border, background: theme.toolbar.itemHover }}>
                {invalidSavedConfig ? (
                    <div role="alert" className="text-xs leading-5" style={{ color: theme.node.muted }}>
                        不支持的相机或镜头配置，请用上下箭头或滚轮重新选择后再应用。
                    </div>
                ) : (
                    <>
                        <div className="text-[var(--fs-label)] uppercase tracking-wide" style={{ color: theme.node.faint }}>当前配置</div>
                        <div className="mt-0.5 text-xs">{summary}</div>
                    </>
                )}
            </div>

            {/* 操作区：重置与复制不产生写入，只有应用会提交生成配置。 */}
            <div className="flex min-h-12 flex-wrap items-center gap-2 border-t px-4 py-2" style={{ borderColor: theme.toolbar.border }}>
                <button type="button" className={secondaryButtonClass} style={{ color: theme.node.muted }} onClick={handleReset}>
                    <RotateCcw className="size-3.5" />重置参数
                </button>
                <span className="flex-1" />
                <Tooltip title="复制当前生成的摄像机提示词">
                    <button type="button" className={secondaryButtonClass} disabled={!enabled || invalidSavedConfig} onClick={() => copyText(buildPrompt(), "摄像机提示词已复制")}>复制提示词</button>
                </Tooltip>
                {/* 关闭后仍保留参数选择，但提交空提示词，便于用户无损恢复。 */}
                <div className="flex items-center gap-2" title={enabled ? "当前启用摄像机控制" : "当前关闭摄像机控制"}>
                    <span className="text-xs transition-colors" style={{ color: enabled ? theme.accent.primary : theme.node.muted, fontWeight: enabled ? 600 : 400 }}>
                        {enabled ? "开启" : "关闭"}
                    </span>
                    <Switch size="sm" checked={enabled} onChange={setEnabled} aria-label="启用摄像机控制" />
                </div>
                <motion.button
                    type="button"
                    disabled={invalidSavedConfig}
                    whileHover={reducedMotion || invalidSavedConfig ? undefined : { y: -1 }}
                    whileTap={reducedMotion || invalidSavedConfig ? undefined : { scale: 0.97 }}
                    className="canvas-node-camera-apply flex h-7 items-center gap-2 rounded-[var(--dock-item-radius)] border px-4 text-[var(--fs-label)] font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-45"
                    style={{ borderColor: theme.node.activeStroke, color: theme.node.activeStroke, background: "transparent" }}
                    onClick={handleApply}
                >
                    <CameraIcon className="size-3.5" />应用
                </motion.button>
            </div>
        </SpotlightSurface>
    );
}
