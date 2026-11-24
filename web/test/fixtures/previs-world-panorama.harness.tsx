import { expect, mock, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { Scene } from "three";

// SSR has no R3F renderer; retain the actual world component and Three objects.
mock.module("@react-three/fiber", () => ({
    useThree: () => ({ scene: new Scene(), invalidate: () => undefined }),
    useFrame: () => undefined,
}));

const { PrevisWorldView } = await import("../../src/components/canvas/previs/previs-world-view");
const { PREVIS_DEFAULT_WORLD } = await import("../../src/lib/canvas/previs/previs-world");

test("panorama removes the actual sky element while keeping world ground, horizon, and lights", () => {
    const normal = renderToStaticMarkup(<PrevisWorldView world={PREVIS_DEFAULT_WORLD} renderMode="beauty" />);
    const panorama = renderToStaticMarkup(<PrevisWorldView world={PREVIS_DEFAULT_WORLD} environment={{ mode: "panorama", url: "/panorama.png" }} renderMode="beauty" />);

    // drei Sky renders a Three primitive; the world terrain uses ordinary meshes.
    expect(normal).toContain("<primitive");
    expect(panorama).not.toContain("<primitive");
    expect(panorama).toContain("<planeGeometry");
    expect(panorama).toContain("<mesh geometry=");
    expect(panorama).toContain("<hemisphereLight");
    expect(panorama).toContain("<directionalLight");
});

test("panorama keeps precipitation and control passes still exclude precipitation", () => {
    const world = { ...PREVIS_DEFAULT_WORLD, weather: "rain" as const };
    const environment = { mode: "panorama" as const, url: "/panorama.png" };
    const beauty = renderToStaticMarkup(<PrevisWorldView world={world} environment={environment} renderMode="beauty" />);
    const depth = renderToStaticMarkup(<PrevisWorldView world={world} environment={environment} renderMode="depth" />);

    expect(beauty).toContain("<lineSegments");
    expect(depth).not.toContain("<lineSegments");
    expect(depth).toContain("<planeGeometry");
});
