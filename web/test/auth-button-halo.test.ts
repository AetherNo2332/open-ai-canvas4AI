import { expect, test } from "bun:test";
import { createAuthButtonHalo } from "../src/pages/auth/auth-button-halo";

function button(left: number) {
    const properties = new Map<string, string>();
    const element = {
        dataset: {} as Record<string, string>,
        style: { setProperty: (key: string, value: string) => properties.set(key, value), removeProperty: (key: string) => properties.delete(key) },
        getBoundingClientRect: () => ({ left, top: 100, width: 400, height: 56 }),
    } as unknown as HTMLElement;
    return { element, properties };
}

test("halo coordinates are local to the button and disappear outside its bounds", () => {
    const halo = createAuthButtonHalo();
    const target = button(200);
    halo.move(target.element, 230, 120);
    expect(target.properties.get("--auth-halo-x")).toBe("30px");
    expect(target.properties.get("--auth-halo-y")).toBe("20px");
    expect(target.element.dataset.authHalo).toBe("true");
    halo.move(target.element, 230, 170);
    expect(target.element.dataset.authHalo).toBeUndefined();
    expect(target.properties.size).toBe(0);
});

test("switching buttons and leaving never leaves a halo on the previous button", () => {
    const halo = createAuthButtonHalo();
    const first = button(200);
    const next = button(700);
    halo.move(first.element, 240, 120);
    halo.move(next.element, 740, 120);
    expect(first.element.dataset.authHalo).toBeUndefined();
    expect(first.properties.size).toBe(0);
    expect(next.element.dataset.authHalo).toBe("true");
    halo.move(null, 1000, 500);
    expect(next.element.dataset.authHalo).toBeUndefined();
    expect(next.properties.size).toBe(0);
    halo.hide();
});
