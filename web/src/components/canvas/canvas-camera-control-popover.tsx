import { useState } from "react";
import { Camera } from "lucide-react";

import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import type { CanvasTheme } from "@/lib/canvas-theme";
import type { CameraControlOptions } from "@/lib/canvas/camera-prompt-library";
import { CanvasNodeCameraPanel } from "./canvas-node-camera-dialog";

type CanvasCameraControlPopoverProps = {
    cameraControl?: CameraControlOptions;
    onCameraControlChange: (options: CameraControlOptions, cameraPrompt?: string) => void;
    theme: CanvasTheme;
    compact?: boolean;
};

export function CanvasCameraControlPopover({ cameraControl, onCameraControlChange, theme, compact = false }: CanvasCameraControlPopoverProps) {
    const [open, setOpen] = useState(false);
    const cameraEnabled = cameraControl?.enabled === true;
    return (
        <>
            <button
                type="button"
                className={`canvas-node-composer-camera-tools-trigger inline-flex h-11 min-w-11 shrink-0 items-center justify-center gap-1 rounded-full px-1.5 text-[var(--fs-tiny)] transition-colors focus-visible:!outline focus-visible:!outline-2 focus-visible:!outline-offset-2 sm:min-w-0 ${compact ? "is-compact sm:h-7" : "sm:h-8"}`}
                style={{
                    background: cameraEnabled ? `${theme.node.activeStroke}66` : theme.node.fill,
                    color: cameraEnabled ? theme.node.panel : theme.node.text,
                    outlineColor: theme.node.activeStroke,
                }}
                aria-pressed={cameraEnabled}
                aria-label="摄影机控制"
                title={`摄影机提示词${cameraEnabled ? " · 已启用" : ""}`}
                onClick={() => setOpen(true)}
            >
                <Camera className="size-3.5" />
                {!compact ? <span>摄影机</span> : null}
            </button>
            {open ? (
                <AppModal title={null} closable={false} open centered footer={null} width={780} flush onCancel={() => setOpen(false)}>
                    <CanvasNodeCameraPanel
                        cameraControl={cameraControl}
                        onClose={() => setOpen(false)}
                        onConfirm={(options, prompt) => {
                            onCameraControlChange(options, prompt);
                            setOpen(false);
                        }}
                    />
                </AppModal>
            ) : null}
        </>
    );
}
