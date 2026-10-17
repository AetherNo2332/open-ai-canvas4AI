export function SnapshotJson({ value, limit = 30000 }: { value: unknown; limit?: number }) {
    const text = value === undefined ? "（缺失）" : JSON.stringify(value, null, 2);
    return (
        <pre className="tool-snapshot-json" tabIndex={0}>
            {text.slice(0, limit)}
            {text.length > limit ? "\n…内容过长，导出可查看全文" : ""}
        </pre>
    );
}
