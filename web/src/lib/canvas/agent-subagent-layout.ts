export function computeSubagentOffsets(count: number, _avatarSize: number, gap: number, expandedIndex: number, nameWidth: (index: number) => number): number[] {
    const valid = expandedIndex >= 0 && expandedIndex < count;
    const offset = valid ? nameWidth(expandedIndex) + gap : 0;
    return Array.from({ length: count }, (_, index) => (valid && index > expandedIndex ? offset : 0));
}
