export type HostConnection = { endpoint: string; kind: 'local' | 'lan'; profileId?: string; principalId?: string };
export type SessionIdentity = { instanceId: string; profileId: string; principal: { id: string }; features?: unknown; drawingEngine?: unknown; runtimeLimits?: unknown; capabilities: { protocol: number; saveMode: string } };
export type SessionApplyOptions = { storageScope: string; remoteAutoSync: false; awaitHydration: true };

export function createDesktopSessionController<S extends SessionIdentity, A extends { user: { id: string } | null }>(deps: {
    readBootstrap: (connection: HostConnection, signal: AbortSignal) => Promise<S>;
    readAuth: (connection: HostConnection, signal: AbortSignal) => Promise<A>;
    clear: () => Promise<void>;
    apply: (payload: A, options: SessionApplyOptions) => Promise<void>;
}) {
    let active: S | null = null;
    let generation = 0;
    let abort: AbortController | null = null;
    return {
        current: () => active,
        async connect(connection: HostConnection, signal?: AbortSignal): Promise<S> {
            const current = ++generation;
            abort?.abort();
            abort = new AbortController();
            const requestSignal = signal ? AbortSignal.any([signal, abort.signal]) : abort.signal;
            active = null;
            await deps.clear();
            requestSignal.throwIfAborted();
            const bootstrap = await deps.readBootstrap(connection, requestSignal);
            const auth = await deps.readAuth(connection, requestSignal);
            requestSignal.throwIfAborted();
            if (current !== generation) throw new DOMException('host changed', 'AbortError');
            if (!auth.user || !bootstrap.profileId || bootstrap.principal.id !== auth.user.id ||
                (connection.profileId && connection.profileId !== bootstrap.profileId) ||
                (connection.principalId && connection.principalId !== auth.user.id)) throw new Error('desktop_identity_mismatch');
            if (bootstrap.capabilities.protocol !== 1) throw new Error('desktop_protocol_mismatch');
            await deps.apply({...auth, features: bootstrap.features, drawingEngine: bootstrap.drawingEngine, runtimeLimits: bootstrap.runtimeLimits},
                {storageScope: `${bootstrap.profileId}:${auth.user.id}`, remoteAutoSync: false, awaitHydration: true});
            requestSignal.throwIfAborted();
            if (current !== generation) throw new DOMException('host changed', 'AbortError');
            active = bootstrap;
            return bootstrap;
        },
    };
}
