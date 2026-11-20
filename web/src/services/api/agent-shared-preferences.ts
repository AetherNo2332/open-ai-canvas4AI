import { http } from "./request";

export type AgentSharedPreferences = {
    revision: number;
    language?: string;
    tone?: "neutral" | "professional" | "casual";
    verbosity?: "concise" | "balanced" | "detailed";
    format?: "auto" | "prose" | "bullets";
};

const path = "/agent/settings/shared-preferences";

export function getAgentSharedPreferences(signal?: AbortSignal) {
    return http.get<AgentSharedPreferences>(path, { signal, timeout: 15_000 });
}

export function saveAgentSharedPreferences(preferences: AgentSharedPreferences) {
    return http.put<AgentSharedPreferences>(path, preferences, { timeout: 15_000 });
}
