import { Color, NearestFilter, PerspectiveCamera, Vector2, WebGLRenderTarget, type Scene, type WebGLRenderer } from 'three';
import { DepthVideoMaterial } from './depth-video-material.ts';
import { suggestDepthRange } from './depth-video.ts';
import { lensOverscan, lensProjection, lensSource, overscanCamera } from './lens-projection.ts';

/** Small on-demand GPU probe: includes posed/skinned surfaces; no per-frame readback. */
export class DepthRangeSampler {
    private target = new WebGLRenderTarget(128, 128, { minFilter: NearestFilter, magFilter: NearestFilter, samples: 0 });
    private material = new DepthVideoMaterial(true);
    private camera = new PerspectiveCamera();
    sample(renderer: WebGLRenderer, scene: Scene, camera: PerspectiveCamera) {
        const width = 128, height = Math.max(32, Math.round(width / camera.aspect));
        this.target.setSize(width, height);
        const pixels = new Uint8Array(width * height * 4), distances: number[] = [];
        const lens = lensProjection(camera), scale = lensOverscan(lens), shot = overscanCamera(camera, scale, this.camera);
        const target = renderer.getRenderTarget(), background = scene.background, material = scene.overrideMaterial;
        const clear = renderer.getClearColor(new Color()), alpha = renderer.getClearAlpha(), autoClear = renderer.autoClear, shadows = renderer.shadowMap.enabled;
        try {
            scene.overrideMaterial = this.material; scene.background = null;
            renderer.shadowMap.enabled = false; renderer.autoClear = true; renderer.setClearColor(0, 0);
            renderer.setRenderTarget(this.target); this.material.withParticles(scene, () => renderer.render(scene, shot));
            renderer.readRenderTargetPixels(this.target, 0, 0, width, height, pixels);
            const point = new Vector2();
            for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) {
                point.set((x + .5) / width * 2 - 1, (y + .5) / height * 2 - 1);
                const source = lensSource(point, camera.aspect, lens).divideScalar(scale);
                const sx = Math.floor((source.x + 1) * .5 * width), sy = Math.floor((source.y + 1) * .5 * height);
                if (sx < 0 || sx >= width || sy < 0 || sy >= height) continue;
                const i = (sy * width + sx) * 4;
                if (pixels[i + 3] !== 255) continue;
                distances.push((pixels[i] / 255 + pixels[i + 1] / 65025 + pixels[i + 2] / 16581375) * 2000);
            }
            return { ...suggestDepthRange(distances), samples: distances.length };
        } finally {
            scene.overrideMaterial = material; scene.background = background;
            renderer.setClearColor(clear, alpha); renderer.autoClear = autoClear; renderer.shadowMap.enabled = shadows; renderer.setRenderTarget(target);
        }
    }
    dispose() { this.material.dispose(); this.target.dispose(); }
}
