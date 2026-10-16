import assert from "node:assert/strict";
import test from "node:test";

// Bun 直接执行 TypeScript 测试时需要保留扩展名；生产 tsconfig 不包含 test/。
import { DEFAULT_VIDEO_PROMPT_MAX_CHARS, defaultModelCapabilityConfig, normalizeModelCapabilityConfig, normalizeVideoValue } from "../src/lib/model-capabilities.ts";
import { imageResolutionTiers } from "../src/lib/image-size-presets.ts";

test("Kemei Seedream defaults retain official 1K/1.5K/2K sizes without a 4K preset", () => {
    for (const model of ["doubao-seedream-5-0-pro-260628", "doubao-seedream-5-0-flash-260915"]) {
        const image = defaultModelCapabilityConfig("km-kemei-seedream", model).image!;
        assert.equal(image.references.maxImages, 10);
        assert.equal(image.references.maskSupported, false);
        assert.equal(image.maxOutputs, 1);
        assert.equal(image.size.default, "2048x2048");
        assert.equal(image.quality.supported, false);
        assert.equal(image.size.presets!.length, 24);
        assert.deepEqual([...new Set(image.size.presets!.map((p) => p.tier))], ["1k", "1.5k", "2k"]);
        assert.equal(image.size.presets!.find((p) => p.size === "2816x1584")?.tier, "2k");
        assert.ok(imageResolutionTiers(image).includes("1.5k"));
    }
    assert.deepEqual(imageResolutionTiers(defaultModelCapabilityConfig("openai-image").image!), ["1k", "2k", "4k"]);
});

test("automatic output reserve ignores legacy manual values without inventing a model ceiling", () => {
    const profile = defaultModelCapabilityConfig();
    Object.assign(profile.text!, { contextWindowTokens: 128000, maxOutputTokens: 8192, reservedOutputTokens: 64000 });
    assert.equal(normalizeModelCapabilityConfig(profile).text!.reservedOutputTokens, 8192);
    profile.text!.maxOutputTokens = 0;
    const normalized = normalizeModelCapabilityConfig(profile).text!;
    assert.equal(normalized.reservedOutputTokens, 16000);
    assert.equal(normalized.maxOutputTokens, 0);
    profile.text!.contextWindowTokens = 1000000;
    assert.equal(normalizeModelCapabilityConfig(profile).text!.reservedOutputTokens, 32768);
    profile.text!.contextWindowTokens = 0;
    assert.equal(normalizeModelCapabilityConfig(profile).text!.reservedOutputTokens, 0);
});

test("text multimodal capability is not guessed from a model name", () => {
    for (const model of ["gpt-4o", "gemini-2.5-pro", "doubao-seed"]) {
        const text = defaultModelCapabilityConfig(undefined, model).text!;
        assert.equal(text.references.maxImages, 0);
        assert.equal(text.references.maxVideos, 0);
    }
});

test("MiniMax H3 and METASO offer every duration from 4 to 30 seconds", () => {
    for (const protocol of ["minimax-video", "metaso-h3"]) {
        const profile = defaultModelCapabilityConfig(protocol, "MiniMax-H3").video!;
        assert.deepEqual(
            profile.duration.values,
            Array.from({ length: 27 }, (_, index) => index + 4),
        );
        assert.equal(profile.duration.default, 5);
        for (const seconds of ["4", "15", "16", "20", "30"]) {
            assert.equal(normalizeVideoValue(profile, { seconds, ratio: "16:9", resolution: "768P" }).seconds, seconds);
        }
    }
});

test("switching to MiniMax H3 replaces an unsupported 720p value with 768P", () => {
    const profile = defaultModelCapabilityConfig("minimax-video", "MiniMax-H3").video!;

    assert.deepEqual(normalizeVideoValue(profile, { seconds: "11", ratio: "16:9", resolution: "720" }), {
        seconds: "11",
        ratio: "16:9",
        resolution: "768P",
    });
});

// 视频提示词由「输入框文本 + 连线内容 + 技能上下文」合成，技能上下文预算为 32000，
// 合成结果远长于用户手输内容。默认上限过小会把正常可用的画布工作流拦在本地预检。
// 这里锁定默认值本身，避免被改回偏小值（前端放行/后端拒绝的判定必须同源）。
test("video prompt default allows a composed canvas prompt", () => {
    assert.equal(DEFAULT_VIDEO_PROMPT_MAX_CHARS, 8000);
    for (const protocol of [undefined, "seedance-videos-compatible", "agnes-video", "volcengine-ark-video"]) {
        const profile = defaultModelCapabilityConfig(protocol, "test-model");
        assert.equal(profile.video!.references.promptMaxChars, DEFAULT_VIDEO_PROMPT_MAX_CHARS);
    }
});

test("raising the video default leaves text and image limits untouched", () => {
    // 只放宽视频默认值，避免顺带改变其它能力的判定口径。
    const profile = defaultModelCapabilityConfig("seedance-videos-compatible", "sd-2.5");
    assert.equal(profile.text!.references.promptMaxChars, 32000);
    assert.equal(profile.image!.references.promptMaxChars, 32000);
});
