import { expect, test } from "bun:test";
import type { KeyboardEvent } from "react";
import { handleDockSwitchKeyDown, type FloatingDockSwitch } from "../src/components/ui/aceternity/floating-dock";

for (const [key, value, expected] of [["ArrowRight", "pan", "select"], ["ArrowLeft", "pan", "select"], ["ArrowRight", "select", "pan"]]) {
    test(`${key} from ${value} moves both selection and focus to ${expected}`, () => {
        let selected = value;
        let focused = value;
        let prevented = false;
        const radios = ["pan", "select"].map(value => ({ focus: () => { focused = value; } }));
        const event = { key, preventDefault: () => { prevented = true; }, currentTarget: { querySelectorAll: () => radios } } as unknown as KeyboardEvent<HTMLElement>;
        const entry: FloatingDockSwitch = { kind: "switch", id: "pointer", label: "指针模式", value,
            options: [{ id: "pan", value: "pan", label: "抓手", icon: null }, { id: "select", value: "select", label: "框选", icon: null }],
            onChange: value => { selected = value; } };
        handleDockSwitchKeyDown(event, entry);
        expect(selected).toBe(expected);
        expect(focused).toBe(expected);
        expect(prevented).toBe(true);
    });
}

test("other keys leave dock selection and native keyboard behavior unchanged", () => {
    const event = { key: "Tab", preventDefault: () => { throw new Error("Tab must remain native"); } } as unknown as KeyboardEvent<HTMLElement>;
    const entry: FloatingDockSwitch = { kind: "switch", id: "pointer", label: "指针模式", value: "pan", options: [], onChange: () => { throw new Error("Tab must not select"); } };
    handleDockSwitchKeyDown(event, entry);
});
