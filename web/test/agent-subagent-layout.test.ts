import { expect, test } from "bun:test";
import { computeSubagentOffsets } from "../src/lib/canvas/agent-subagent-layout";

test("collapsed subagents stay at their normal positions", () => {
    expect(computeSubagentOffsets(4, 44, 8, -1, () => 70)).toEqual([0, 0, 0, 0]);
});
test("expansion moves only the avatars to its right by the measured label plus gap", () => {
    expect(computeSubagentOffsets(4, 44, 8, 1, () => 70)).toEqual([0, 0, 78, 78]);
    expect(computeSubagentOffsets(1, 44, 8, 0, () => 70)).toEqual([0]);
    expect(computeSubagentOffsets(0, 44, 8, -1, () => 70)).toEqual([]);
});
