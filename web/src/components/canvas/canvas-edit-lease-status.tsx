import { LockKeyhole, RefreshCw } from "lucide-react";

export function CanvasEditLeaseStatus({ message, phase, onRetry }: { message: string; phase: string; onRetry?: () => void }) {
    const retryable = ["denied", "offline", "expired", "saved", "idle"].includes(phase);
    return (
        <div className="flex min-w-0 items-center gap-1.5 rounded-md bg-muted/40 px-2 py-1 text-xs text-muted-foreground" role="status" data-canvas-edit-status={phase}>
            <LockKeyhole className="size-3 shrink-0" aria-hidden />
            <span className="min-w-0 flex-1 break-words">{message}</span>
            {retryable && onRetry ? (
                <button type="button" className="inline-flex shrink-0 items-center gap-1 rounded px-1 py-0.5 text-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring" onClick={onRetry} aria-label="重新获取编辑占用">
                    <RefreshCw className="size-3" />
                    继续编辑
                </button>
            ) : null}
        </div>
    );
}
