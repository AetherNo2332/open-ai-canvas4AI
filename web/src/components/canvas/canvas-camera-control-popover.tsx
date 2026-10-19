import { useState } from "react";
import { Camera } from "lucide-react";

import { AppModal } from "@/components/ui/product/app-modal/app-modal";
import type { CameraControlOptions } from "@/lib/canvas/camera-prompt-library";
import { CanvasNodeCameraPanel, type CameraControlPreview } from "./canvas-node-camera-dialog";

type CanvasCameraControlPopoverProps = {
    cameraControl?: CameraControlOptions;
    onCameraControlChange: (options: CameraControlOptions) => void;
    preview?: CameraControlPreview;
    compact?: boolean;
};

export function CanvasCameraControlPopover({ cameraControl, onCameraControlChange, preview, compact = false }: CanvasCameraControlPopoverProps) {
    const [open, setOpen] = useState(false);
    const cameraEnabled = cameraControl?.enabled === true;
    return (
        <>
            <button
                type="button"
                className={`camera-control-trigger ${compact ? "is-compact" : ""}`}
                data-enabled={cameraEnabled}
                aria-expanded={open}
                aria-haspopup="dialog"
                aria-label={`摄影机控制${cameraEnabled ? "，已启用" : ""}`}
                title={`摄影机控制${cameraEnabled ? " · 已启用" : ""}`}
                onClick={() => setOpen(true)}
            >
                <Camera className="size-3.5" />
                {!compact ? <span>摄影机</span> : null}
            </button>
            {open ? (
                <AppModal title={null} aria-label="摄影机控制" closable={false} open centered footer={null} width="min(960px, calc(100vw - 24px))" flush onCancel={() => setOpen(false)}>
                    <CanvasNodeCameraPanel
                        cameraControl={cameraControl}
                        preview={preview}
                        onClose={() => setOpen(false)}
                        onConfirm={(options) => {
                            onCameraControlChange(options);
                            setOpen(false);
                        }}
                    />
                </AppModal>
            ) : null}
        </>
    );
}
