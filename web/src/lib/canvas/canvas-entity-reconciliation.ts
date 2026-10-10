export function canvasEntityReconciler(nodes: { id: string }[], connections: { id: string }[]) {
    const nodeIds = new Set(nodes.map((node) => node.id));
    const connectionIds = new Set(connections.map((connection) => connection.id));
    return {
        nodeId: (id: string | null) => (id && nodeIds.has(id) ? id : null),
        connectionId: (id: string | null) => (id && connectionIds.has(id) ? id : null),
        nodeSelection: (selected: Set<string>) => {
            if ([...selected].every((id) => nodeIds.has(id))) return selected;
            return new Set([...selected].filter((id) => nodeIds.has(id)));
        },
    };
}
