import { NoBlending, Points, ShaderMaterial, type Scene, type IUniform } from 'three';

/** Depth variants retain the exact particle vertex animation and live uniform objects. */
export class DepthParticles {
    private materials = new Map<ShaderMaterial, { material: ShaderMaterial; release: () => void }>();
    private uniforms: Record<string, IUniform>;
    private fragment: string;
    constructor(uniforms: Record<string, IUniform>, fragment: string) { this.uniforms = uniforms; this.fragment = fragment; }

    render<T>(scene: Scene, draw: () => T): T {
        const restored: { points: Points; material: ShaderMaterial }[] = [];
        try {
            scene.traverse(object => {
                if (!(object instanceof Points) || !object.userData.directorParticle || !(object.material instanceof ShaderMaterial)) return;
                const source = object.material;
                let entry = this.materials.get(source);
                if (!entry) {
                    const material = new ShaderMaterial({
                        vertexShader: source.vertexShader,
                        fragmentShader: this.fragment,
                        uniforms: { ...source.uniforms, ...this.uniforms, particleSoftness: { value: source.userData.particleSoftness } },
                        defines: { ...source.defines },
                        depthTest: true, depthWrite: true, transparent: false, blending: NoBlending,
                    });
                    material.allowOverride = false; material.toneMapped = false;
                    const release = () => { source.removeEventListener('dispose', release); material.dispose(); this.materials.delete(source); };
                    source.addEventListener('dispose', release);
                    entry = { material, release }; this.materials.set(source, entry);
                }
                restored.push({ points: object, material: source }); object.material = entry.material;
            });
            return draw();
        } finally { for (const item of restored) item.points.material = item.material; }
    }

    dispose() { for (const entry of [...this.materials.values()]) entry.release(); }
}
