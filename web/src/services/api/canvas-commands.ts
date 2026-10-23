import { http, captureHTTP } from "./request";
import type { Command, Receipt, Snapshot, TaskHandle, MaterializationStatus, Page as MediaVersionPage } from "../../lib/canvas/command-contract.generated";
import type { CommandTransport, CommandEffect } from "../canvas-command-session";
import type { CanvasCapabilities } from "../../lib/canvas/canvas-editor-commands";
import { captureCanvasEditProofProvider } from "../canvas-edit-leases";

type CommandHTTP = Pick<typeof http, "get" | "post">;
const canvasPath = (id: string) => "/desktop/canvases/" + encodeURIComponent(id);
const taskPath = (id: string) => "/desktop/tasks/" + encodeURIComponent(id);
const identity = (command: Command) => ({ documentEpoch: command.documentEpoch, clientId: command.clientId, operationId: command.operationId });
export const readCanvasCapabilities = (api: CommandHTTP = captureHTTP(), signal?: AbortSignal) => api.get<CanvasCapabilities>("/desktop/capabilities", { signal });
export function createCanvasCommandTransport(api: CommandHTTP = captureHTTP(), proofs = captureCanvasEditProofProvider()): CommandTransport {
    const receipt = (command: Command, signal?: AbortSignal) =>
        api.get<Receipt>(canvasPath(command.canvasId) + "/receipts/" + encodeURIComponent(command.operationId), { params: { clientId: command.clientId, documentEpoch: command.documentEpoch }, signal });
    return {
        snapshot: (id, signal) => api.get<Snapshot>(canvasPath(id) + "/snapshot", { signal }),
        events: (id, after, signal) => api.get<Receipt[]>(canvasPath(id) + "/events", { params: { after, limit: 250 }, signal }),
        execute: async (command, signal) => api.post<Receipt>(canvasPath(command.canvasId) + "/commands", command, { signal, headers: await proofs(command) }),
        receipt,
        async invoke(command: Command, effect: CommandEffect, signal?: AbortSignal) {
            const headers = await proofs(command, effect.type === "restore" || effect.type === "undo" || effect.type === "redo");
            switch (effect.type) {
                case "task":
                    return api.post<TaskHandle>(canvasPath(command.canvasId) + "/tasks", { ...effect.request, command }, { signal, headers });
                case "previs":
                    return api.post<TaskHandle>(canvasPath(command.canvasId) + "/previs-tasks", { ...effect.request, command }, { signal, headers });
                case "undo":
                case "redo":
                    return api.post<Receipt>(canvasPath(command.canvasId) + "/" + effect.type, identity(command), { signal, headers });
                case "resolve_uncertainty":
                    return api.post<Receipt>(taskPath(effect.taskId) + "/uncertainty/resolve", { command }, { signal, headers });
                case "restore":
                    return api.post<Snapshot>(canvasPath(command.canvasId) + "/restore", { ...identity(command), expectedRevision: effect.expectedRevision, document: effect.document }, { signal, headers }).then(() => receipt(command, signal));
            }
        },
        recover(command, effect, signal) {
            if (effect.type === "task" || effect.type === "previs")
                return api.get<TaskHandle>(canvasPath(command.canvasId) + "/task-submissions/" + encodeURIComponent(command.operationId), { params: { clientId: command.clientId, documentEpoch: command.documentEpoch }, signal });
            return receipt(command, signal);
        },
    };
}
export const readTaskMaterialization = (taskId: string, signal?: AbortSignal) => http.get<MaterializationStatus>(taskPath(taskId) + "/materialization", { signal });
export const readCanvasMediaVersions = (canvasId: string, nodeId: string, beforeVersion = 0, signal?: AbortSignal) =>
    http.get<MediaVersionPage>(canvasPath(canvasId) + "/nodes/" + encodeURIComponent(nodeId) + "/media-versions", { params: beforeVersion ? { beforeVersion } : undefined, signal });
export const retryTaskMaterialization = (taskId: string, signal?: AbortSignal) => http.post<MaterializationStatus>(taskPath(taskId) + "/materialization/retry", undefined, { signal });
export function createCanvasHistoryReader(api: CommandHTTP = captureHTTP()) {
    return (canvasId: string, documentEpoch: string, clientId: string, signal?: AbortSignal) => api.get<{ canUndo: boolean; canRedo: boolean }>(canvasPath(canvasId) + "/history-state", { params: { documentEpoch, clientId }, signal });
}
