import { $, escape } from './common.ts';
import type { AppContext } from '../app-context.ts';
import { requestCanvasExport, canvasExportScope, type CanvasExportKind } from '../host/export-channel.ts';

/** 云端导出面板：只提交意图，任务、资源和权限由画布宿主负责。 */
export function createVideoPanel(ctx: AppContext) {
    const documentOf = () => ctx.scenes.document();
    const activeShot = () => canvasExportScope(documentOf()).activeShotId;

    async function request(kind: CanvasExportKind) {
        if (ctx.busy) return;
        ctx.busy = true;
        ctx.playing = false;
        ctx.updateTimeUI();
        try {
            const result = await requestCanvasExport({ document: documentOf(), kind, shotId: activeShot() });
            if (result.status === 'succeeded') ctx.toast(kind === 'shot-table' ? `已写入 ${result.rows ?? 0} 行镜头表` : '已写入画布');
            else ctx.toast(`导出任务已提交：${result.taskId ?? '任务中心'}`);
        } catch (error) {
            ctx.toast(error instanceof Error ? error.message : '导出失败', true);
        } finally {
            ctx.busy = false;
            ctx.updateTimeUI();
        }
    }

    async function snapshot() {
        await request('composition-frame');
    }

    function exportDialog() {
        if (ctx.busy) return;
        let scope;
        try {
            scope = canvasExportScope(documentOf());
        } catch (error) {
            ctx.toast(error instanceof Error ? error.message : '当前场景不可导出', true);
            return;
        }
        ctx.showModal(
            '导出到画布',
            `<div class="export-layout cloud-export-layout">
                <p class="export-summary">导出任务由影策任务中心执行，完成后自动写回当前画布。</p>
                <label class="field"><span>当前镜头</span><select id="export-shot">${scope.shots.map((shot) => `<option value="${escape(shot.id)}" ${shot.id === scope.activeShotId ? 'selected' : ''}>${escape(shot.name)} · ${shot.duration}s</option>`).join('')}</select></label>
                <div class="button-row">
                    <button type="button" id="export-composition">写入构图帧</button>
                    <button type="button" id="export-shot-table">写入镜头表</button>
                </div>
                <p id="export-summary" class="export-summary" aria-live="polite">白膜视频会在任务完成后写入画布。</p>
            </div>`,
            '<button type="button" data-act="close-modal" class="subtle">取消</button><button type="button" data-act="export-start" class="primary">提交白膜视频</button>',
        );
        const shot = $<HTMLSelectElement>('#export-shot');
        const send = (kind: CanvasExportKind) => {
            void requestCanvasExport({ document: documentOf(), kind, shotId: shot.value })
                .then((result) => ctx.toast(result.status === 'succeeded' ? '已写入画布' : `导出任务已提交：${result.taskId ?? '任务中心'}`))
                .catch((error) => ctx.toast(error instanceof Error ? error.message : '导出失败', true));
        };
        $('#export-composition').addEventListener('click', () => send('composition-frame'));
        $('#export-shot-table').addEventListener('click', () => send('shot-table'));
    }

    function updateExportSummary() {
        const summary = document.querySelector<HTMLElement>('#export-summary');
        if (summary) summary.textContent = '导出内容只写回画布节点与结构化分镜表，不生成本地文件。';
    }

    async function startExport() {
        if (ctx.busy) return;
        const shot = document.querySelector<HTMLSelectElement>('#export-shot');
        if (shot) {
            try {
                const result = await requestCanvasExport({ document: documentOf(), kind: 'whitebox-video', shotId: shot.value });
                ctx.toast(`导出任务已提交：${result.taskId ?? '任务中心'}`);
            } catch (error) {
                ctx.toast(error instanceof Error ? error.message : '导出失败', true);
            }
            return;
        }
        await request('whitebox-video');
    }

    return { snapshot, exportDialog, updateExportSummary, startExport };
}
