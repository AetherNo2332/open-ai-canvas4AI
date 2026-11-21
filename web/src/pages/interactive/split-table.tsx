import { Button, Checkbox, Input, Select, Tag } from "antd";
import type { SplitIssue, SplitRow } from "@/lib/interactive/split-types";

interface SplitTableProps {
    rows: SplitRow[];
    issues: SplitIssue[];
    onChange: (rows: SplitRow[]) => void;
}

/** 拆分表校对：行内编辑、错误高亮；去向选项目标只允许本表行键。 */
export function SplitTable({ rows, issues, onChange }: SplitTableProps) {
    const errorRows = new Set(issues.filter((issue) => issue.severity === "error").map((issue) => issue.rowKey));
    const rowKeyOptions = rows.map((row) => ({ value: row.rowKey, label: row.rowKey }));
    const patch = (index: number, change: Partial<SplitRow>) => onChange(rows.map((row, current) => (current === index ? { ...row, ...change } : row)));

    return (
        <div className="split-table-wrap">
            <table className="split-table">
                <thead>
                    <tr>
                        <th>行键</th>
                        <th>标题</th>
                        <th>正文</th>
                        <th>角色</th>
                        <th>场景</th>
                        <th>去向</th>
                        <th>结局</th>
                        <th>建议</th>
                    </tr>
                </thead>
                <tbody>
                    {rows.map((row, index) => (
                        <tr key={`${row.rowKey}:${index}`} data-split-row={row.rowKey} className={errorRows.has(row.rowKey) ? "is-error" : ""}>
                            <td>
                                <Input size="small" value={row.rowKey} onChange={(event) => patch(index, { rowKey: event.target.value.trim() })} />
                            </td>
                            <td>
                                <Input size="small" value={row.title} placeholder="≤12 字" onChange={(event) => patch(index, { title: event.target.value })} />
                            </td>
                            <td>
                                <Input.TextArea size="small" autoSize={{ minRows: 2, maxRows: 6 }} value={row.body} onChange={(event) => patch(index, { body: event.target.value })} />
                            </td>
                            <td>
                                <Input size="small" value={row.characters.join("、")} placeholder="角色名，逗号分隔" onChange={(event) => patch(index, { characters: splitNames(event.target.value) })} />
                            </td>
                            <td>
                                <Input size="small" value={row.scenes.join("、")} placeholder="场景名，逗号分隔" onChange={(event) => patch(index, { scenes: splitNames(event.target.value) })} />
                            </td>
                            <td>
                                <div className="split-choices">
                                    {row.choices.map((choice, choiceIndex) => (
                                        <div className="split-choice" key={choiceIndex}>
                                            <Input
                                                size="small"
                                                value={choice.text}
                                                placeholder="选项文案"
                                                onChange={(event) => patch(index, { choices: row.choices.map((item, current) => (current === choiceIndex ? { ...item, text: event.target.value } : item)) })}
                                            />
                                            <Select
                                                size="small"
                                                className="split-choice__target"
                                                value={choice.target || undefined}
                                                placeholder="目标"
                                                options={rowKeyOptions}
                                                onChange={(target) => patch(index, { choices: row.choices.map((item, current) => (current === choiceIndex ? { ...item, target } : item)) })}
                                            />
                                            <Button size="small" type="text" onClick={() => patch(index, { choices: row.choices.filter((_, current) => current !== choiceIndex) })}>
                                                删
                                            </Button>
                                        </div>
                                    ))}
                                    <Button size="small" type="dashed" block disabled={row.choices.length >= 20} onClick={() => patch(index, { choices: [...row.choices, { text: "", target: "" }] })}>
                                        加选项
                                    </Button>
                                    {row.choices.length === 0 ? (
                                        <Select size="small" allowClear className="split-choice__next" value={row.next || undefined} placeholder="线性后继" options={rowKeyOptions} onChange={(next) => patch(index, { next: next ?? "" })} />
                                    ) : null}
                                </div>
                            </td>
                            <td>
                                <Checkbox checked={row.ending} onChange={(event) => patch(index, { ending: event.target.checked, choices: event.target.checked ? [] : row.choices, next: "" })} />
                            </td>
                            <td>{row.suggested ? <Tag color="gold">建议</Tag> : null}</td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );
}

function splitNames(value: string): string[] {
    return value
        .split(/[,，、;；]/)
        .map((item) => item.trim())
        .filter(Boolean);
}
