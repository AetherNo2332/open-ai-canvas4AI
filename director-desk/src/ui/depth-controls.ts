import type { AppContext } from '../app-context.ts';
import type { Project } from '../model.ts';
import { DEFAULT_DEPTH_VIDEO, assertDepthVideo } from '../cinematography/depth-video.ts';
import './depth-controls.css';

export function renderDepthControls(project: Project) {
    const host = document.querySelector('#depth-controls'); if (!host) return;
    const d = project.depthVideo ?? DEFAULT_DEPTH_VIDEO;
    host.innerHTML = `<select data-depth="enabled" aria-label="摄影机画面"><option value="false" ${!d.enabled?'selected':''}>普通画面</option><option value="true" ${d.enabled?'selected':''}>深度画面</option></select>
        <div class="depth-range" ${d.enabled?'':'hidden'}><label>近 / m<input data-depth="near" aria-label="深度近端" type="number" min="0" max="2000" step=".1" value="${d.near}"></label><label>远 / m<input data-depth="far" aria-label="深度远端" type="number" min=".1" max="2000" step=".1" value="${d.far}"></label><label>曲线<input data-depth="curve" aria-label="深度曲线" type="number" min=".25" max="4" step=".25" value="${d.curve ?? 1}"></label><label><input data-depth="invert" type="checkbox" ${d.invert?'checked':''}>反转</label><button type="button" data-depth-fit="selection">取选中范围</button><button type="button" data-depth-fit="frame">取画面范围</button></div>`;
}
export function installDepthControls(ctx: AppContext) {
    document.querySelector('#depth-controls')!.addEventListener('click', event => {
        const button = (event.target as HTMLElement).closest<HTMLElement>('[data-depth-fit]');
        if (!button || ctx.busy || ctx.history.pending || ctx.draft) return;
        ctx.playing = false;
        try {
            const { near, far } = ctx.engine.suggestDepthRange(ctx.time, ctx.preview, button.dataset.depthFit === 'selection' ? [ctx.selected] : undefined);
            ctx.change(() => { ctx.project.depthVideo = { ...(ctx.project.depthVideo ?? DEFAULT_DEPTH_VIDEO), near, far }; }, false);
        } catch (error) { ctx.toast((error as Error).message, true); }
    });
    document.querySelector('#depth-controls')!.addEventListener('change', event => {
        const input = event.target as HTMLInputElement, key = input.dataset.depth;
        if (!key || ctx.busy || ctx.history.pending || ctx.draft) { renderDepthControls(ctx.project); return; }
        const d = { ...(ctx.project.depthVideo ?? DEFAULT_DEPTH_VIDEO), [key]: key === 'enabled' ? input.value === 'true' : key === 'invert' ? input.checked : Number(input.value) };
        try { assertDepthVideo(d); ctx.change(() => { ctx.project.depthVideo = d; }, false); }
        catch (error) { ctx.toast((error as Error).message, true); renderDepthControls(ctx.project); }
    });
}
