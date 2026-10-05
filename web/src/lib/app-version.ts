export function formatBuildVersion(version: string, commit: string): string {
    const [base, legacyCommit] = version.trim().split("+", 2);
    const revision = commit && commit !== "unknown" ? commit : legacyCommit;
    return revision ? `${base} (${revision.slice(0, 7)})` : base;
}
