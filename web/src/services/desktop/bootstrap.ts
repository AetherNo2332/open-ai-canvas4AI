import type { AuthSessionPayload } from '@/services/api/auth';
import type { CanvasDrawingEngineSetting } from '@/lib/canvas/canvas-drawing-engine';
import type { FeatureAvailability } from '@/stores/use-user-store';
import type { RuntimeLimits } from '@/services/api/auth';
import { http, setAPIHost } from '@/services/api/request';
import { createDesktopSessionController, type HostConnection } from './session-controller';

export type DesktopSession = {
    instanceId: string;
    profileId: string;
    principal: { id: string; ownerId: string; clientId: string; role: string; scopes: string[] };
    features: FeatureAvailability;
    runtimeLimits: RuntimeLimits;
    drawingEngine: CanvasDrawingEngineSetting;
    capabilities: { protocol: number; saveMode: string; mcp: boolean; collaboration: boolean };
};

const controller = createDesktopSessionController<DesktopSession, AuthSessionPayload>({
    readBootstrap: async(connection, signal) => {
        setAPIHost(connection.endpoint);
        return http.get<DesktopSession>('/desktop/bootstrap', {signal});
    },
    readAuth: (_connection, signal) => http.get<AuthSessionPayload>('/auth/session', {signal}),
    clear: async() => {
        const [{useUserStore},{appQueryClient},{stopLegacySingleWriter}] = await Promise.all([
            import('@/stores/use-user-store'),import('@/lib/query-client'),import('./legacy-single-writer'),
        ]);
        await stopLegacySingleWriter();
        useUserStore.getState().clearSession();
        appQueryClient.clear();
    },
    apply: async(payload,options) => {
        const {applyUserSession} = await import('@/lib/user-session');
        await applyUserSession(payload,options);
    },
});

export async function bootstrapDesktopSession(signal?: AbortSignal): Promise<DesktopSession> {
    return controller.connect({endpoint: window.location.origin,kind:'local'},signal);
}

export async function connectHost(connection: HostConnection): Promise<void> {
    await controller.connect(connection);
}

export const currentDesktopSession = () => controller.current();
