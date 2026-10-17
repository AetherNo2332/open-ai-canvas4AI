import type { PrevisScene, PrevisTransform, PrevisVec3 } from "@/types/previs";
import { previsInspectorViewModel } from "@/lib/canvas/previs/previs-inspector";
import "./director-inspector.css";

export type DirectorInspectorProps = {
    scene: PrevisScene;
    selectedEntityId: string;
    activeShotId: string;
    onUpdateObject: (id: string, patch: Partial<PrevisTransform>) => void;
    onUpdateCamera: (id: string, patch: { focalLength?: number; target?: PrevisVec3 }) => void;
};

const AXIS_LABELS = ["X", "Y", "Z"] as const;

function Vec3Fields({ label, value, onCommit }: { label: string; value: PrevisVec3; onCommit: (next: PrevisVec3) => void }) {
    return (
        <div className="dps-fields" role="group" aria-label={label}>
            <span className="dps-fields__label">{label}</span>
            {value.map((axis, index) => (
                <input
                    key={`${label}-${index}-${axis}`}
                    className="dps-axis"
                    type="number"
                    step={0.1}
                    aria-label={`${label} ${AXIS_LABELS[index]}`}
                    defaultValue={axis}
                    onBlur={(event) => {
                        const parsed = Number(event.target.value);
                        const next = [...value] as PrevisVec3;
                        next[index] = Number.isFinite(parsed) ? parsed : value[index];
                        if (next[index] !== value[index]) onCommit(next);
                    }}
                    onKeyDown={(event) => {
                        if (event.key === "Enter") (event.target as HTMLInputElement).blur();
                    }}
                />
            ))}
        </div>
    );
}

/**
 * DirectorDesk 原生检查器（导演编辑器 scope，P1）：
 * 选中对象的位姿（位置/旋转/缩放）+ 活动镜头机位（焦段/目标点）。
 * 纯展示 + 回调；保存语义由持有协调器的容器负责。
 */
export function DirectorInspector({ scene, selectedEntityId, activeShotId, onUpdateObject, onUpdateCamera }: DirectorInspectorProps) {
    const vm = previsInspectorViewModel(scene, selectedEntityId, activeShotId);
    const object = vm.object;
    const camera = vm.camera;

    if (!object && !camera) {
        return (
            <div className="dps-root">
                <div className="dps-empty" role="status">
                    选择对象或切换镜头
                </div>
            </div>
        );
    }

    return (
        <div className="dps-root">
            {object ? (
                <section className="dps-section" aria-label="对象检查器">
                    <header className="dps-section__header">
                        <span className="dps-section__label">对象</span>
                        <strong className="dps-section__name">{object.name}</strong>
                    </header>
                    <Vec3Fields label="位置" value={object.position} onCommit={(position) => onUpdateObject(object.id, { position })} />
                    <Vec3Fields label="旋转" value={object.rotation} onCommit={(rotation) => onUpdateObject(object.id, { rotation })} />
                    <Vec3Fields label="缩放" value={object.scale} onCommit={(scale) => onUpdateObject(object.id, { scale })} />
                </section>
            ) : null}
            {camera ? (
                <section className="dps-section" aria-label="摄影机检查器">
                    <header className="dps-section__header">
                        <span className="dps-section__label">摄影机</span>
                        <strong className="dps-section__name">{camera.name}</strong>
                    </header>
                    <label className="dps-field">
                        <span className="dps-fields__label">焦段（mm）</span>
                        <input
                            className="dps-focal"
                            type="number"
                            min={1}
                            max={300}
                            step={1}
                            defaultValue={camera.focalLength}
                            key={`focal-${camera.id}-${camera.focalLength}`}
                            aria-label="焦段（mm）"
                            onBlur={(event) => {
                                const parsed = Number(event.target.value);
                                if (Number.isFinite(parsed) && parsed !== camera.focalLength) onUpdateCamera(camera.id, { focalLength: parsed });
                            }}
                            onKeyDown={(event) => {
                                if (event.key === "Enter") (event.target as HTMLInputElement).blur();
                            }}
                        />
                    </label>
                    <Vec3Fields label="目标点" value={camera.target} onCommit={(target) => onUpdateCamera(camera.id, { target })} />
                </section>
            ) : null}
        </div>
    );
}
