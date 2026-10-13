import { download } from '../storage.ts';
import type { ExportJob } from './plan.ts';
import type { ExportDestination } from './batch.ts';

/**
 * 云端导出投递。
 *
 * 桌面版的「默认目录 / 另存为 / 选择文件夹」依赖 Electron 文件服务,已随桌面能力剥离;
 * 云端只保留浏览器下载这一条本地投递路径,**导出到画布**(白膜视频、构图帧、镜头参数、
 * 分镜表)由 host bridge 交给影策任务与资源链路(T7)。
 */
export type SaveMode = 'download';

export async function videoDestination(_mode: SaveMode, _jobs: ExportJob[], _outputDirectory?: string): Promise<ExportDestination> {
    return {
        async open() {
            return undefined;
        },
        async save(job, blob) {
            if (blob) download(blob, job.filename);
            return job.filename;
        },
        async discard() {
            // 浏览器下载没有可回滚的中间态。
        },
    };
}
