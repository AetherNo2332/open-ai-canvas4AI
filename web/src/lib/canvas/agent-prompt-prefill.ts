/**
 * 把画布投递的文本追加到 Agent 草稿末尾（同一段落，用空格分隔）。
 * 空草稿和非空草稿都写入去掉首尾空白的文本；草稿末尾已经是同一段文本时不再重复追加（重复点击“发给 Agent”）。
 */
export function appendAgentPromptPrefill(current: string, prefill: string) {
    const value = prefill.trim();
    if (!value) return current;
    const draft = current.trimEnd();
    if (!draft) return value;
    if (draft === value || (draft.endsWith(value) && /\s/.test(draft.charAt(draft.length - value.length - 1)))) return current;
    const separator = current.endsWith(" ") ? "" : " ";
    return `${current}${separator}${value}`;
}
