interface AgentToolCountProps {
    count?: number;
    loading?: boolean;
}

export default function AgentToolCount({ count, loading = false }: AgentToolCountProps) {
    const available = Number.isSafeInteger(count) && count !== undefined && count >= 0;
    return (
        <section id="tools" className="agent-settings-section flex flex-wrap items-center justify-between gap-4" aria-labelledby="agent-tools-heading" aria-busy={loading}>
            <div className="space-y-2">
                <h2 id="agent-tools-heading" className="text-base font-medium">
                    当前工具总数
                </h2>
                <p className="text-sm text-foreground/60">平台已提供的工具，实际可用范围随任务和权限确定。</p>
            </div>
            <output aria-label="当前工具总数" className="text-3xl font-light tabular-nums">
                {loading ? "读取中…" : available ? count : "暂不可用"}
            </output>
        </section>
    );
}
