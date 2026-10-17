import { expect, test } from "bun:test";
import { createAuthMotionPlayer } from "../src/pages/auth/auth-motion";
import type { animate } from "animejs";

function harness() {
    let reduced = false;
    const calls: { targets: unknown; parameters: Parameters<typeof animate>[1]; reverts: number }[] = [];
    const driver = ((targets, parameters) => {
        const record = { targets, parameters, reverts: 0 };
        calls.push(record);
        return {
            revert: () => {
                record.reverts += 1;
            },
        };
    }) as typeof animate;
    const player = createAuthMotionPlayer({ driver, reducedMotion: () => reduced, normalDuration: 150, fastDuration: 120 });
    return {
        player,
        calls,
        reduce: () => {
            reduced = true;
        },
    };
}
const element = () => ({}) as HTMLElement;

test("entrance stages controls and cleans all animation styles on unmount", () => {
    const { player, calls } = harness();
    const root = element();
    const controls = [element(), element(), element()];
    player.enter(root, controls);
    expect(calls).toHaveLength(2);
    expect(calls[0].targets).toBe(root);
    expect(calls[0].parameters.x).toEqual([14, 0]);
    expect(calls[1].targets).toBe(controls);
    const delay = calls[1].parameters.delay as (target: HTMLElement, index: number, targets: HTMLElement[]) => number;
    const firstDelay = delay(controls[0], 0, controls);
    expect(delay(controls[1], 1, controls)).toBeGreaterThan(firstDelay);
    player.enter(root, [element()], false);
    expect(calls).toHaveLength(3); // Late fields enter without replaying the whole form.
    player.dispose();
    expect(calls.every((call) => call.reverts === 1)).toBe(true);
});

test("button feedback returns to its size and native invalid events shake only once per batch", () => {
    const { player, calls } = harness();
    const button = element();
    player.press(button);
    expect(calls[0].parameters.scale).toEqual([1, 0.98, 1]);
    player.press(button);
    expect(calls[0].reverts).toBe(1);
    const form = element();
    player.invalid(form, 0);
    player.invalid(form, 10);
    expect(calls).toHaveLength(3);
    expect(calls[2].parameters.x).toEqual([0, -5, 4, -3, 2, 0]);
    player.invalid(form, 400);
    expect(calls).toHaveLength(4);
    expect(calls[2].reverts).toBe(1);
    player.dispose();
});

test("reduced motion skips all effects and cancels pending effects when toggled", () => {
    const { player, calls, reduce } = harness();
    player.enter(element(), [element()]);
    reduce();
    player.dispose();
    player.enter(element(), [element()]);
    player.press(element());
    player.invalid(element());
    expect(calls).toHaveLength(2);
    expect(calls.every((call) => call.reverts === 1)).toBe(true);
});
