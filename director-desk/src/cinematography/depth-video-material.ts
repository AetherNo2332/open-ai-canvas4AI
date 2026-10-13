import { DoubleSide, MeshDepthMaterial, type Scene } from 'three';
import type { DepthRange } from './depth-video.ts';
import { DepthParticles } from './depth-particles.ts';

const depthDeclarations = `uniform float displayNear, displayFar, displayCurve;
uniform bool displayInvert;
varying float displayDistance;`;
const depthOutput = (sampleDistances: boolean) => sampleDistances ? `float d = clamp(displayDistance / 2000.0, 0.0, 0.9999999);
    vec3 packed = fract(d * vec3(1.0, 255.0, 65025.0));
    packed -= packed.yzz * vec3(1.0 / 255.0, 1.0 / 255.0, 0.0);
    gl_FragColor = vec4(packed, 1.0);` : `float gray = 1.0 - clamp((displayDistance - displayNear) / (displayFar - displayNear), 0.0, 1.0);
    gray = pow(gray, displayCurve);
    if (displayInvert) gray = 1.0 - gray;
    gl_FragColor = vec4(vec3(gray), 1.0);`;

/** Write display depth before MSAA resolves coverage, preserving the raw depth buffer. */
export class DepthVideoMaterial extends MeshDepthMaterial {
    private readonly sampleDistances: boolean;
    private readonly particles: DepthParticles;
    private readonly rangeUniforms = {
        displayNear: { value: .1 }, displayFar: { value: 30 },
        displayInvert: { value: false },
        displayCurve: { value: 1 },
    };

    constructor(sampleDistances = false) {
        super({ side: DoubleSide });
        this.sampleDistances = sampleDistances;
        this.toneMapped = false;
        this.particles = new DepthParticles(this.rangeUniforms, `${depthDeclarations}
uniform float opacity, particleSoftness;
varying float fade;
void main() {
    float radius = length(gl_PointCoord - .5) * 2.;
    if (radius > 1.) discard;
    // Depth has no color blending: retain visible particle coverage, discard transparent edges.
    if (pow(1. - radius, particleSoftness) * opacity * fade < .01) discard;
    ${depthOutput(sampleDistances)}
}`);
        this.onBeforeCompile = shader => {
            Object.assign(shader.uniforms, this.rangeUniforms);
            // Keep Three's skinning, morphing, instancing and displacement vertex path.
            shader.vertexShader = 'varying float displayDistance;\n' + shader.vertexShader.replace(
                '#include <project_vertex>', '#include <project_vertex>\n displayDistance = -mvPosition.z;',
            );
            shader.fragmentShader = depthDeclarations + '\n' + shader.fragmentShader.replace(
                'gl_FragColor = vec4( vec3( 1.0 - fragCoordZ ), opacity );',
                depthOutput(sampleDistances),
            );
        };
    }

    override customProgramCacheKey() { return this.sampleDistances ? 'director-depth-sampling-v1' : 'director-display-depth-curve-v2'; }

    withParticles<T>(scene: Scene, draw: () => T) { return this.particles.render(scene, draw); }
    override dispose() { this.particles.dispose(); super.dispose(); }

    setRange(range: DepthRange) {
        this.rangeUniforms.displayNear.value = range.near;
        this.rangeUniforms.displayFar.value = range.far;
        this.rangeUniforms.displayInvert.value = range.invert;
        this.rangeUniforms.displayCurve.value = range.curve ?? 1;
    }
}
