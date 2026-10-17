import { http } from "./request";
import type { ChannelModel } from "./wallet";
import type { ModelCapabilityConfig } from "@/lib/model-capabilities";
import type { ProtocolCapability, ModelProtocolDefinition } from "@/lib/model-protocols";

export type DesktopChannel = { id: string; name: string; baseUrl: string; enabled: boolean; models: ChannelModel[]; hasApiKey: boolean; hasSecretKey: boolean; hasHeaders: boolean };
const channels = "/desktop/settings/channels";
export const listDesktopChannels = () => http.get<{ channels: DesktopChannel[] }>(channels);
export const saveDesktopChannel = (id: string | undefined, input: unknown) => (id ? http.patch(`${channels}/${encodeURIComponent(id)}`, input) : http.post(channels, input));
export const deleteDesktopChannel = (id: string) => http.delete(`${channels}/${encodeURIComponent(id)}`);
export const getDesktopPrivateHosts = () => http.get<{ hosts: string[] }>("/desktop/settings/private-hosts");
export const saveDesktopPrivateHosts = (hosts: string[]) => http.put("/desktop/settings/private-hosts", { hosts });
export const previewDesktopModels = (id: string) => http.get<{ models: string[] }>(`${channels}/${encodeURIComponent(id)}/catalog`);
export type DesktopModelInput = { modelKey: string; providerModelKey?: string; displayName?: string; description?: string; capability: ProtocolCapability; protocol: string; enabled: boolean; capabilityConfig?: ModelCapabilityConfig };
export const saveDesktopModel = (channelId: string, id: string | undefined, input: DesktopModelInput) => {
    const path = `${channels}/${encodeURIComponent(channelId)}/models`;
    return id ? http.patch(`${path}/${encodeURIComponent(id)}`, input) : http.post(path, input);
};
export const deleteDesktopModel = (channelId: string, id: string) => http.delete(`${channels}/${encodeURIComponent(channelId)}/models/${encodeURIComponent(id)}`);
export const testDesktopModel = (channelId: string, input: DesktopModelInput) => http.post<{ durationMs: number }>(`${channels}/${encodeURIComponent(channelId)}/test`, input);
export async function getDesktopProtocols(): Promise<ModelProtocolDefinition[]> {
    const data = await http.get<{ protocols: Array<{ id: string; name: string; vendor: string; categories: ProtocolCapability[]; create: string; poll?: string; contentType: string; enabled: boolean }> }>("/desktop/settings/protocols");
    return data.protocols.flatMap((item) =>
        item.categories.map((capability) => ({ value: item.id, label: item.name, vendor: item.vendor, capability, create: item.create, poll: item.poll, contentType: item.contentType, media: "", enabled: item.enabled })),
    );
}
