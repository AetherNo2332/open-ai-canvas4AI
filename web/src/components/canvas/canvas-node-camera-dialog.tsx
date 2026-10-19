import { useId, useMemo, useState } from "react";
import { Button, Select, Slider, Switch } from "antd";
import { Camera, Copy, RotateCcw, X } from "lucide-react";

import { CachedResourceImage } from "@/components/cached-resource-image";
import { useCopyText } from "@/hooks/use-copy-text";
import { APERTURES, APERTURE_META, CAMERA_PROFILES, FOCAL_LENGTHS, FOCAL_LENGTH_META, LENS_PROFILES, buildCameraPrompt, type CameraControlOptions } from "@/lib/canvas/camera-prompt-library";
import "./canvas-node-camera-dialog.css";

const defaultCameraControl: CameraControlOptions = { enabled: false, camera: "arri_alexa_mini_lf", lens: "arri_signature_prime", focalLength: 50, aperture: 2.8 };
const cameraOptions = CAMERA_PROFILES.map((profile) => ({ value: profile.id, label: profile.label }));
const lensOptions = LENS_PROFILES.map((profile) => ({ value: profile.id, label: profile.label }));

export type CameraControlPreview = { src?: string; storageKey?: string; title?: string };

function CameraParameter({
    label,
    value,
    values,
    format,
    description,
    useCase,
    start,
    end,
    onChange,
}: {
    label: string;
    value: number;
    values: readonly number[];
    format: (value: number) => string;
    description?: string;
    useCase?: string;
    start: string;
    end: string;
    onChange: (value: number) => void;
}) {
    return (
        <section className="camera-control-parameter" aria-label={`${label}设置`}>
            <div className="camera-control-parameter-heading">
                <span>{label}</span>
                <output aria-live="polite">{format(value)}</output>
            </div>
            <Slider
                min={0}
                max={values.length - 1}
                step={1}
                value={Math.max(0, values.indexOf(value))}
                onChange={(index) => onChange(values[index])}
                ariaLabelForHandle={label}
                ariaValueTextFormatterForHandle={(index) => format(values[index])}
                tooltip={{ formatter: (index) => (index === undefined ? "" : format(values[index])) }}
            />
            <div className="camera-control-scale" aria-hidden="true">
                <span>{start}</span>
                <span>{end}</span>
            </div>
            <p className="camera-control-help">
                {description}
                {useCase ? ` · 适合${useCase}` : ""}
            </p>
        </section>
    );
}

/** 左侧展示当前素材与配置，右侧编辑草稿；只有应用会写回生成参数。 */
export function CanvasNodeCameraPanel({ cameraControl, preview, onClose, onConfirm }: { cameraControl?: CameraControlOptions; preview?: CameraControlPreview; onClose: () => void; onConfirm: (options: CameraControlOptions, prompt: string) => void }) {
    const titleId = useId();
    const cameraId = useId();
    const lensId = useId();
    const switchId = useId();
    const copyText = useCopyText();
    const [draft, setDraft] = useState<CameraControlOptions>(() => ({ ...defaultCameraControl, ...cameraControl }));
    const camera = CAMERA_PROFILES.find((profile) => profile.id === draft.camera);
    const lens = LENS_PROFILES.find((profile) => profile.id === draft.lens);
    const focal = FOCAL_LENGTH_META[draft.focalLength];
    const aperture = APERTURE_META[draft.aperture];
    const checked = useMemo(() => {
        try {
            return { prompt: buildCameraPrompt({ cameraId: draft.camera, lensId: draft.lens, focalLengthMm: draft.focalLength, apertureF: draft.aperture }), error: undefined };
        } catch (reason) {
            return { prompt: "", error: reason instanceof Error ? reason.message : "摄影机参数不可用，请重新选择" };
        }
    }, [draft.camera, draft.lens, draft.focalLength, draft.aperture]);
    const update = <Key extends keyof CameraControlOptions>(key: Key, value: CameraControlOptions[Key]) => setDraft((current) => ({ ...current, [key]: value }));
    const hasPreview = Boolean(preview?.src || preview?.storageKey);
    const placeholder = (
        <div className="camera-control-placeholder">
            <Camera size={72} strokeWidth={1} aria-hidden="true" />
            <span>拍摄设定</span>
            <p>选好机身与镜头，为下一次生成设定画面风格。</p>
        </div>
    );

    return (
        <section className="camera-control-panel" aria-labelledby={titleId} data-canvas-no-zoom data-canvas-wheel-scroll onPointerDown={(event) => event.stopPropagation()} onMouseDown={(event) => event.stopPropagation()}>
            <header className="camera-control-header">
                <div>
                    <p className="camera-control-eyebrow">CAMERA SETTINGS</p>
                    <h2 id={titleId}>摄影机控制</h2>
                    <p className="camera-control-subtitle">选择机身与镜头，调整焦距和光圈。</p>
                </div>
                <div className="camera-control-header-actions">
                    <Button type="text" icon={<RotateCcw size={16} aria-hidden="true" />} onClick={() => setDraft({ ...defaultCameraControl })}>
                        重置
                    </Button>
                    <Button type="text" icon={<X size={18} aria-hidden="true" />} aria-label="关闭摄影机控制" onClick={onClose} />
                </div>
            </header>

            <div className="camera-control-body">
                <aside className="camera-control-preview" aria-label="当前素材与拍摄配置">
                    <div className="camera-control-viewfinder">
                        <div className="camera-control-preview-heading">
                            <span>{hasPreview ? "当前图片" : "拍摄设定"}</span>
                            <span>{draft.enabled ? "已启用" : "未启用"}</span>
                        </div>
                        <div className="camera-control-image">
                            {hasPreview ? <CachedResourceImage src={preview?.src} storageKey={preview?.storageKey} alt={preview?.title || "当前图片"} width={480} height={360} eager fallback={placeholder} /> : placeholder}
                        </div>
                        <div className="camera-control-readout">
                            <span>
                                {draft.focalLength}
                                <small>mm</small>
                            </span>
                            <span>f/{draft.aperture}</span>
                        </div>
                    </div>
                    <p className="camera-control-preview-note">参数用于下一次生成，此处仅展示当前图片与设置。</p>
                    <dl className="camera-control-summary">
                        <div>
                            <dt>机身</dt>
                            <dd>{camera?.label || "请选择有效机身"}</dd>
                        </div>
                        <div>
                            <dt>镜头</dt>
                            <dd>{lens?.label || "请选择有效镜头"}</dd>
                        </div>
                        <div>
                            <dt>焦距</dt>
                            <dd>
                                {draft.focalLength}mm{focal ? ` · ${focal.zhName}` : ""}
                            </dd>
                        </div>
                        <div>
                            <dt>光圈</dt>
                            <dd>
                                f/{draft.aperture}
                                {aperture ? ` · ${aperture.zhName}` : ""}
                            </dd>
                        </div>
                    </dl>
                </aside>

                <div className="camera-control-fields">
                    <div className="camera-control-equipment">
                        <label htmlFor={cameraId}>机身</label>
                        <Select id={cameraId} aria-label="摄影机机身" showSearch optionFilterProp="label" value={draft.camera} options={cameraOptions} onChange={(value) => update("camera", value)} />
                        <p className="camera-control-help">{camera?.description || "当前机身已不可用，请重新选择。"}</p>
                    </div>
                    <div className="camera-control-equipment">
                        <label htmlFor={lensId}>镜头</label>
                        <Select id={lensId} aria-label="摄影镜头" showSearch optionFilterProp="label" value={draft.lens} options={lensOptions} onChange={(value) => update("lens", value)} />
                        <p className="camera-control-help">{lens?.description || "当前镜头已不可用，请重新选择。"}</p>
                    </div>
                    <CameraParameter
                        label="焦距"
                        value={draft.focalLength}
                        values={FOCAL_LENGTHS}
                        format={(value) => `${value}mm`}
                        description={focal?.description}
                        useCase={focal?.useCase}
                        start="14mm · 广角"
                        end="200mm · 长焦"
                        onChange={(value) => update("focalLength", value)}
                    />
                    <CameraParameter
                        label="光圈"
                        value={draft.aperture}
                        values={APERTURES}
                        format={(value) => `f/${value}`}
                        description={aperture?.description}
                        useCase={aperture?.useCase}
                        start="f/1.2 · 浅景深"
                        end="f/16 · 深景深"
                        onChange={(value) => update("aperture", value)}
                    />
                    {checked.error ? (
                        <div role="alert" className="camera-control-error">
                            {checked.error}。请重新选择，或点击「重置」恢复默认参数。
                        </div>
                    ) : null}
                </div>
            </div>

            <footer className="camera-control-footer">
                <div className="camera-control-enabled">
                    <Switch id={switchId} checked={draft.enabled} onChange={(value) => update("enabled", value)} aria-label="启用摄影机控制" />
                    <div>
                        <label htmlFor={switchId}>用于下一次图片生成</label>
                        <p>{draft.enabled ? "应用后，四项参数会加入生成提示词。" : "当前未启用，应用后仍会保留所选参数。"}</p>
                    </div>
                </div>
                <div className="camera-control-actions">
                    <Button type="text" icon={<Copy size={15} aria-hidden="true" />} disabled={!draft.enabled || Boolean(checked.error)} onClick={() => copyText(checked.prompt, "摄影机提示词已复制")}>
                        复制提示词
                    </Button>
                    <Button onClick={onClose}>取消</Button>
                    <Button
                        type="primary"
                        disabled={Boolean(checked.error)}
                        onClick={() => {
                            if (!checked.error) onConfirm({ ...draft }, draft.enabled ? checked.prompt : "");
                        }}
                    >
                        应用到当前生成
                    </Button>
                </div>
            </footer>
        </section>
    );
}
