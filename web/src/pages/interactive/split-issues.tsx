import { Alert } from "antd";
import type { SplitIssue } from "@/lib/interactive/split-types";

interface SplitIssuesProps {
    issues: SplitIssue[];
    onLocate: (rowKey: string) => void;
}

/** 行级问题清单：点击定位到行；全局问题没有行键时只展示。 */
export function SplitIssues({ issues, onLocate }: SplitIssuesProps) {
    if (issues.length === 0) return <Alert type="success" showIcon message="校验通过，可以导入" />;
    const errors = issues.filter((issue) => issue.severity === "error");
    const warnings = issues.filter((issue) => issue.severity === "warning");
    return (
        <div className="split-issues">
            {errors.length > 0 ? (
                <Alert
                    type="error"
                    showIcon
                    message={`${errors.length} 个错误，修正后才能导入`}
                    description={
                        <ul>
                            {errors.map((issue, index) => (
                                <li key={`error-${index}`}>
                                    <button type="button" className="split-issue-link" disabled={!issue.rowKey} onClick={() => issue.rowKey && onLocate(issue.rowKey)}>
                                        {issue.rowKey || "全表"}
                                    </button>
                                    <span>{issue.message}</span>
                                </li>
                            ))}
                        </ul>
                    }
                />
            ) : null}
            {warnings.length > 0 ? (
                <Alert
                    type="warning"
                    showIcon
                    message={`${warnings.length} 个提示，可带病导入`}
                    description={
                        <ul>
                            {warnings.map((issue, index) => (
                                <li key={`warning-${index}`}>
                                    <button type="button" className="split-issue-link" disabled={!issue.rowKey} onClick={() => issue.rowKey && onLocate(issue.rowKey)}>
                                        {issue.rowKey || "全表"}
                                    </button>
                                    <span>{issue.message}</span>
                                </li>
                            ))}
                        </ul>
                    }
                />
            ) : null}
        </div>
    );
}
