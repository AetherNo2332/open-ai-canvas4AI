export type CanvasNavigationSummary = {
    id: string;
    updatedAt: string;
};

export function latestEditedCanvasId(projects: readonly CanvasNavigationSummary[]): string | null {
    let latest: CanvasNavigationSummary | undefined;
    let latestTimestamp = -Infinity;

    for (const project of projects) {
        const timestamp = Date.parse(project.updatedAt);
        const comparableTimestamp = Number.isFinite(timestamp) ? timestamp : 0;
        if (!latest || comparableTimestamp > latestTimestamp || (comparableTimestamp === latestTimestamp && project.id.localeCompare(latest.id) > 0)) {
            latest = project;
            latestTimestamp = comparableTimestamp;
        }
    }

    return latest?.id || null;
}
