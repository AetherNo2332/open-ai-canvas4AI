import { Box, Folder, FolderOpen, Layers3, Palette, PersonStanding, Theater, UploadCloud, UserRound, Workflow } from "lucide-react";

import { getNodeIcon, getNodeLabel } from "@/lib/canvas/node-registry";
import { registerAddNodeMenuCommands, type AddNodeMenuCommand } from "@/lib/canvas/tool-registry";
import type { ShortplayAssetCategory } from "@/lib/canvas/canvas-shortplay-asset";
import { CanvasNodeType } from "@/types/canvas";

/** 真正创建节点的命令，文案与图标统一取自节点注册表。 */
function nodeCommand(type: CanvasNodeType, rest: Omit<AddNodeMenuCommand, "id" | "label" | "icon" | "section">): AddNodeMenuCommand {
    return { id: type, label: getNodeLabel(type), icon: getNodeIcon(type), section: "node", ...rest };
}

/** 自由画布上的短剧局部卡：只在未关联短剧的画布上出现，收录后才进作品资产。 */
function shortplayAssetCommand(category: ShortplayAssetCategory, label: string, icon: React.ReactNode, order: number): AddNodeMenuCommand {
    return {
        id: `shortplay-asset-${category}`,
        label,
        icon,
        section: "node",
        defaultOrder: order,
        applicable: (ctx) => !ctx.isProjectLinked && Boolean(ctx.handlers.onAddShortplayAsset),
        run: (ctx) => ctx.handlers.onAddShortplayAsset?.(category),
    };
}

export const addNodeMenuCommands: AddNodeMenuCommand[] = [
    // 项目级动作不占用节点网格，创作节点保持统一排列。
    { id: "style", label: "项目画风", icon: <Palette />, section: "project", defaultOrder: 10, applicable: (ctx) => !ctx.isProjectLinked, run: (ctx) => ctx.handlers.onChooseStyle() },
    // 创作节点
    nodeCommand(CanvasNodeType.Text, { defaultOrder: 10, run: (ctx) => ctx.handlers.onAddText() }),
    nodeCommand(CanvasNodeType.Drawing, { defaultOrder: 20, run: (ctx) => ctx.handlers.onAddDrawing() }),
    nodeCommand(CanvasNodeType.Script, { badge: "核心", defaultOrder: 30, run: (ctx) => ctx.handlers.onAddScript() }),
    nodeCommand(CanvasNodeType.Submap, { defaultOrder: 35, run: (ctx) => ctx.handlers.onAddExtensionNode(CanvasNodeType.Submap) }),
    nodeCommand(CanvasNodeType.Frame, { defaultOrder: 40, applicable: (ctx) => ctx.workspaceMode !== "simple", run: (ctx) => ctx.handlers.onAddFrame() }),
    { id: "folder", label: "文件夹", icon: <Folder />, badge: "6 款", section: "node", defaultOrder: 45, run: (ctx) => ctx.handlers.onAddFolder() },
    nodeCommand(CanvasNodeType.Image, { defaultOrder: 50, run: (ctx) => ctx.handlers.onAddImage() }),
    { id: "project-character", label: "角色卡", icon: <UserRound />, section: "node", defaultOrder: 55, run: (ctx) => ctx.handlers.onOpenProjectCharacters() },
    nodeCommand(CanvasNodeType.Video, { defaultOrder: 60, run: (ctx) => ctx.handlers.onAddVideo() }),
    nodeCommand(CanvasNodeType.BatchTable, { defaultOrder: 66, run: (ctx) => ctx.handlers.onAddExtensionNode(CanvasNodeType.BatchTable) }),
    nodeCommand(CanvasNodeType.MediaConversion, { badge: "本地", defaultOrder: 65, run: (ctx) => ctx.handlers.onAddExtensionNode(CanvasNodeType.MediaConversion) }),
    // 世界引擎落在节点分区，但它开的是导演工作台、不是某种画布节点，故不走注册表。
    { id: "previs", label: "世界引擎", icon: <Layers3 />, badge: "3D", section: "node", defaultOrder: 70, applicable: (ctx) => ctx.workspaceMode !== "simple", run: (ctx) => ctx.handlers.onOpenPrevis() },
    nodeCommand(CanvasNodeType.Audio, { defaultOrder: 80, applicable: (ctx) => ctx.workspaceMode !== "simple", run: (ctx) => ctx.handlers.onAddAudio() }),
    // 短剧局部卡：自由画布上先起草角色/道具/场景，再「收录到短剧」进作品资产。
    shortplayAssetCommand("character", "短剧角色卡", <UserRound />, 82),
    shortplayAssetCommand("prop", "短剧道具卡", <Box />, 83),
    shortplayAssetCommand("scene", "短剧场景卡", <Theater />, 84),
    // 云端和本地工作流共用独立配置节点，不进入基础模型节点的渠道选择。
    { id: "workflow", label: "工作流", icon: <Workflow />, section: "workflow", defaultOrder: 10, applicable: (ctx) => ctx.workspaceMode !== "simple", run: (ctx) => ctx.handlers.onAddWorkflow() },
    // 导入资源
    { id: "upload", label: "上传文件", icon: <UploadCloud />, section: "resource", defaultOrder: 10, run: (ctx) => ctx.handlers.onUpload() },
    { id: "assets", label: "素材库", icon: <FolderOpen />, section: "resource", defaultOrder: 30, applicable: (ctx) => !ctx.isProjectLinked, run: (ctx) => ctx.handlers.onOpenMyAssets() },
    { id: "pose-library", label: "姿势库", icon: <PersonStanding />, section: "resource", defaultOrder: 35, applicable: (ctx) => Boolean(ctx.handlers.onOpenPoseLibrary), run: (ctx) => ctx.handlers.onOpenPoseLibrary?.() },
];

registerAddNodeMenuCommands(addNodeMenuCommands);
