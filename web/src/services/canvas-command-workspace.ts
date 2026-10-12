import { CanvasCommandSession, type CommandJournal, type CommandTransport } from "./canvas-command-session";
export function createCanvasCommandWorkspace(deps: { transport: CommandTransport; clientId: string; journal?: CommandJournal; schedule?: (fn: () => void) => () => void }) {
    const sessions = new Map<string, CanvasCommandSession>();
    const opening = new Map<string, Promise<CanvasCommandSession>>();
    const openingSessions = new Set<CanvasCommandSession>();
    const timers = new Set<() => void>();
    let closed = false;
    const schedule =
        deps.schedule ||
        ((fn: () => void) => {
            const timer = setTimeout(fn, 1000);
            return () => clearTimeout(timer);
        });
    function observe(session: CanvasCommandSession) {
        if (closed) return;
        const cancel = schedule(() => {
            timers.delete(cancel);
            if (closed) return;
            void session.reconnect().finally(() => {
                if (!closed) observe(session);
            });
        });
        timers.add(cancel);
    }
    return {
        get: (id: string) => sessions.get(id),
        open(id: string): Promise<CanvasCommandSession> {
            if (closed) return Promise.reject(new Error("canvas_workspace_closed"));
            const existing = sessions.get(id);
            if (existing) return Promise.resolve(existing);
            const active = opening.get(id);
            if (active) return active;
            const session = new CanvasCommandSession(deps.transport, deps.clientId, deps.journal);
            openingSessions.add(session);
            const load = session
                .open(id)
                .then(() => {
                    if (closed) {
                        session.close();
                        throw new Error("canvas_workspace_closed");
                    }
                    sessions.set(id, session);
                    observe(session);
                    return session;
                })
                .catch((error) => {
                    session.close();
                    throw error;
                })
                .finally(() => {
                    opening.delete(id);
                    openingSessions.delete(session);
                });
            opening.set(id, load);
            return load;
        },
        async flush() {
            const results = await Promise.all([...sessions.values()].map((session) => session.flush()));
            return results.every(Boolean);
        },
        async close() {
            closed = true;
            for (const cancel of timers) cancel();
            timers.clear();
            for (const session of [...sessions.values(), ...openingSessions]) session.close();
            sessions.clear();
            openingSessions.clear();
        },
    };
}
export type CanvasCommandWorkspace = ReturnType<typeof createCanvasCommandWorkspace>;
