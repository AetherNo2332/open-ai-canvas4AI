import "./canvas-refresh-shell.css";

export function CanvasRefreshShell() {
    return (
        <main className="canvas-refresh-shell" aria-busy="true" aria-live="polite">
            <div className="canvas-refresh-shell-head">
                <span className="canvas-refresh-shell-eyebrow">Canvas</span>
                <span className="canvas-refresh-shell-label">正在准备画布</span>
            </div>
            <span className="loading-rule" aria-hidden="true">
                <i />
            </span>
            <div className="canvas-refresh-shell-stage" aria-hidden="true" />
        </main>
    );
}
