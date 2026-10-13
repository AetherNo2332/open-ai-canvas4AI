import type { AppContext } from "../app-context.ts";
import { removedCapabilityMessage } from "./desktop-removed.ts";
import { hostBridge } from "./runtime.ts";

/**
 * 云端保存:工程事实源在影策(PrevisScene),保存请求经 host bridge 交给工作台。
 * 未接通宿主时给出显式提示,不写本地文件、不静默成功。
 */
export async function saveProjectViaHost(ctx: AppContext, saveAs = false): Promise<boolean> {
    const bridge = hostBridge();
    if (!bridge) {
        ctx.toast(removedCapabilityMessage("file-save"), true);
        return false;
    }
    try {
        await bridge.request("desk.save.request", { saveAs, projectId: ctx.scenes.projectId });
        ctx.toast(saveAs ? "已请求保存副本" : "已保存");
        return true;
    } catch (error) {
        ctx.toast(error instanceof Error ? error.message : "保存失败", true);
        return false;
    }
}
