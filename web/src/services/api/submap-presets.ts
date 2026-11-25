import { http } from "@/services/api/request";

/** 用户自定义 submap 预制件：一组普通画布节点的局部化快照，按用户隔离。 */
export type UserSubmapPresetNode = {
    localId: string;
    type: string;
    title: string;
    metadata?: Record<string, unknown>;
};

export type UserSubmapPresetSnapshot = {
    version: number;
    id?: string;
    title: string;
    nodes: UserSubmapPresetNode[];
};

export type UserSubmapPreset = {
    id: string;
    userId: string;
    name: string;
    manifestJson: string;
    revision: number;
    createdAt: string;
    updatedAt: string;
};

export function listUserSubmapPresets() {
    return http.get<{ presets: UserSubmapPreset[] }>("/user-submap-presets");
}

export function createUserSubmapPreset(snapshotJson: string) {
    return http.post<{ preset: UserSubmapPreset }>("/user-submap-presets", { manifestJson: snapshotJson });
}

export function updateUserSubmapPreset(id: string, snapshotJson: string) {
    return http.patch<{ preset: UserSubmapPreset }>(`/user-submap-presets/${encodeURIComponent(id)}`, { manifestJson: snapshotJson });
}

export function deleteUserSubmapPreset(id: string) {
    return http.delete<{ id: string }>(`/user-submap-presets/${encodeURIComponent(id)}`);
}
