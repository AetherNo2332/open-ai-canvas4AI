/**
 * Professional cinematography prompt library for the Camera Control panel.
 *
 * 相机/镜头目录、提示词片段与真实产品图整理自 TapNow 相机控制素材（HAR 提取）；
 * 产品图位于 web/public/camera-controls/，与 profile.image 一一对应。
 * The output prompt is a comma-joinable fragment appended to the user's
 * generation prompt so the image model treats it as camera direction.
 */

export interface CameraProfile {
    id: string;
    label: string;
    zhName: string;
    shortTag: string;
    profilePrompt: string;
    /** web/public 下的产品图公共路径。 */
    image: string;
    useCase: string;
    description: string;
}

export interface LensProfile {
    id: string;
    label: string;
    zhName: string;
    shortTag: string;
    profilePrompt: string;
    /** web/public 下的产品图公共路径。 */
    image: string;
    useCase: string;
    description: string;
}

export interface CameraControlOptions {
    enabled: boolean;
    camera: string;
    lens: string;
    focalLength: number;
    aperture: number;
}

export const CAMERA_PROFILES: CameraProfile[] = [
    {
        id: "arri_alexa_65",
        label: "ARRI ALEXA 65",
        zhName: "阿莱 ALEXA 65",
        shortTag: "ARRI ALEXA 65",
        profilePrompt: "shot on ARRI Alexa 65, large-format cinematography, hyper-realistic rendering, cinematic lighting, 8K resolution, movie-still quality",
        image: "/camera-controls/cameras/arri-alexa-65.png",
        description: "大画幅传奇电影机，超写实院线质感。",
        useCase: "院线大片、史诗场景、广告",
    },
    {
        id: "arri_alexa_35",
        label: "ARRI ALEXA 35",
        zhName: "阿莱 ALEXA 35",
        shortTag: "ARRI ALEXA 35",
        profilePrompt: "shot on ARRI ALEXA 35, cinematic color grading, soft highlight roll-off, refined filmic texture",
        image: "/camera-controls/cameras/arri-alexa-35.png",
        description: "阿莱新一代 Super35 电影机，柔和高光与电影调色。",
        useCase: "剧情片、广告、人物叙事",
    },
    {
        id: "sony_venice",
        label: "Sony VENICE",
        zhName: "索尼 VENICE",
        shortTag: "Sony VENICE",
        profilePrompt: "shot on Sony Venice, clean digital look, 8K UHD resolution, refined modern color science",
        image: "/camera-controls/cameras/sony-venice.png",
        description: "全画幅电影机，干净数码质感与 8K 细节。",
        useCase: "广告、纪录片、夜景戏剧",
    },
    {
        id: "red_v_raptor",
        label: "RED V-RAPTOR",
        zhName: "RED V-RAPTOR",
        shortTag: "RED V-RAPTOR",
        profilePrompt: "shot on RED V-RAPTOR, high contrast, sharp focus, crisp modern digital detail",
        image: "/camera-controls/cameras/red-v-raptor.png",
        description: "8K VV 高对比锐利画面，现代数字电影感。",
        useCase: "科幻、动作、高分辨率 VFX",
    },
    {
        id: "panavision_dxl2",
        label: "Panavision DXL2",
        zhName: "潘那维申 DXL2",
        shortTag: "Panavision DXL2",
        profilePrompt: "shot on Panavision DXL2, Hollywood movie style, large-format filmic texture, rich highlight roll-off",
        image: "/camera-controls/cameras/panavision-dxl2.png",
        description: "好莱坞传统电影风格，大画幅油润质感。",
        useCase: "剧情长片、高奢广告",
    },
    {
        id: "arricam_lt",
        label: "ARRICAM LT",
        zhName: "阿莱 ARRICAM LT",
        shortTag: "ARRICAM LT",
        profilePrompt: "shot on Arricam LT, Kodak Vision3 500T, pin-sharp focus, organic medium film grain, soft highlight roll-off, rich color density, faint red halo at the edge of light sources, highly detailed texture",
        image: "/camera-controls/cameras/arricam-lt.png",
        description: "Kodak Vision3 500T 胶片质感，暖调颗粒。",
        useCase: "年代戏、胶片叙事",
    },
    {
        id: "arriflex_435",
        label: "ARRIFLEX 435",
        zhName: "阿莱 ARRIFLEX 435",
        shortTag: "ARRIFLEX 435",
        profilePrompt: "shot on Arriflex 435, Kodak Vision3 250D 5207 style, pin-sharp focus, medium film grain, soft highlight roll-off, high dynamic range, rich color density",
        image: "/camera-controls/cameras/arriflex-435.png",
        description: "Kodak Vision3 250D 日光胶片，细腻颗粒。",
        useCase: "日光外景、胶片质感",
    },
    {
        id: "imax_keighley",
        label: "IMAX Keighley",
        zhName: "IMAX Keighley",
        shortTag: "IMAX Keighley",
        profilePrompt:
            "shot on IMAX Keighley, Kodak Vision3 500T style, extremely sharp focus, high dynamic range, extremely shallow depth of field, 18K resolution, hyper-realistic skin texture, organic film grain, 3D separation, color graded, volumetric lighting, epic scale",
        image: "/camera-controls/cameras/imax-keighley.png",
        description: "IMAX 胶片大画幅，极致景深与史诗感。",
        useCase: "史诗大片、巨幕场景",
    },
    {
        id: "imax_film_camera",
        label: "IMAX Film Camera",
        zhName: "IMAX 胶片机",
        shortTag: "IMAX Film Camera",
        profilePrompt: "shot on IMAX film camera, high resolution, fine film grain, epic large-format scale",
        image: "/camera-controls/cameras/imax-film-camera.png",
        description: "IMAX 胶片摄影机，高分辨率与细颗粒。",
        useCase: "巨幕纪录、大场面",
    },
];

export const LENS_PROFILES: LensProfile[] = [
    {
        id: "arri_signature_prime",
        label: "ARRI Signature Prime",
        zhName: "阿莱 Signature Prime",
        shortTag: "ARRI Signature Prime",
        profilePrompt: "using ARRI Signature Prime lens, large-format coverage, clean image, perfect optics, creamy organic bokeh, smooth highlight rolloff",
        image: "/camera-controls/lenses/arri-signature-prime.png",
        description: "大画幅定焦，干净光学与奶油虚化。",
        useCase: "人物叙事、顶级商业片",
    },
    {
        id: "zeiss_ultra_prime",
        label: "Zeiss Ultra Prime",
        zhName: "蔡司 Ultra Prime",
        shortTag: "Zeiss Ultra Prime",
        profilePrompt: "using Zeiss Ultra Prime lens, clinically sharp, high contrast, clean modern rendering",
        image: "/camera-controls/lenses/zeiss-ultra-prime.png",
        description: "锐利高反差现代定焦。",
        useCase: "科技广告、现代剧情",
    },
    {
        id: "cooke_s4",
        label: "Cooke S4",
        zhName: "库克 S4",
        shortTag: "Cooke S4",
        profilePrompt: "using Cooke S4 lens, The Cooke Look, creamy bokeh, warm flattering color",
        image: "/camera-controls/lenses/cooke-s4.png",
        description: "经典 Cooke Look，奶油虚化、暖调肤色。",
        useCase: "人物、剧情、广告",
    },
    {
        id: "cooke_speed_panchro",
        label: "Cooke Speed Panchro",
        zhName: "库克 Speed Panchro",
        shortTag: "Cooke Speed Panchro",
        profilePrompt: "using Cooke Speed Panchro Series III, buttery background blur, The Cooke Look, warm organic tones, soft sharpness, atmospheric depth",
        image: "/camera-controls/lenses/cooke-speed-panchro.png",
        description: "三代老镜，暖调柔和、氛围感强。",
        useCase: "年代戏、复古叙事",
    },
    {
        id: "cooke_sf_18x",
        label: "Cooke Anamorphic SF 18x",
        zhName: "库克 Anamorphic SF 18x",
        shortTag: "Cooke Anamorphic SF 18x",
        profilePrompt: "using Cooke Anamorphic SF, golden lens flare, oval bokeh, full-frame anamorphic look, warm cinematic rendering",
        image: "/camera-controls/lenses/cooke-sf-18x.png",
        description: "全画幅变形镜，金色光晕与椭圆虚化。",
        useCase: "风格化广告、MV",
    },
    {
        id: "canon_k35",
        label: "Canon K-35",
        zhName: "佳能 K35",
        shortTag: "Canon K-35",
        profilePrompt: "using Canon K-35 lens, soft contrast, golden lens flare, vintage character",
        image: "/camera-controls/lenses/canon-k35.png",
        description: "老镜柔对比与金色光晕。",
        useCase: "复古人像、年代感",
    },
    {
        id: "helios",
        label: "Helios 44-2",
        zhName: "Helios 44-2",
        shortTag: "Helios 44-2",
        profilePrompt: "using Helios 44-2 lens, swirly bokeh, twisted background",
        image: "/camera-controls/lenses/helios.png",
        description: "苏式旋焦老镜，扭曲背景虚化。",
        useCase: "文艺片、风格化特写",
    },
    {
        id: "panavision_c_series",
        label: "Panavision C-Series",
        zhName: "Panavision C-Series",
        shortTag: "Panavision C-Series",
        profilePrompt: "using Panavision C-Series anamorphic lens, anamorphic cinematic look, distinct blue horizontal lens flare, oval bokeh",
        image: "/camera-controls/lenses/panavision-c-series.png",
        description: "经典变形镜，蓝色水平光晕。",
        useCase: "科幻、夜戏、宽银幕",
    },
    {
        id: "panavision_primo",
        label: "Panavision Primo Anamorphic",
        zhName: "Panavision Primo 变形",
        shortTag: "Panavision Primo Anamorphic",
        profilePrompt: "using Panavision Primo Anamorphic lens, oval bokeh, horizontal blue lens flare, 2x squeeze, anamorphic look",
        image: "/camera-controls/lenses/panavision-primo.png",
        description: "2x 变形镜，椭圆虚化与干净中心。",
        useCase: "商业大片、宽银幕叙事",
    },
    {
        id: "hawk_class_x",
        label: "Hawk Class X Anamorphic",
        zhName: "Hawk Class X 变形",
        shortTag: "Hawk Class X Anamorphic",
        profilePrompt: "using Hawk Class X Anamorphic lens, subtle creamy vertical oval bokeh, organic sharpness, vintage-modern aesthetic, soft horizontal flare",
        image: "/camera-controls/lenses/hawk-class-x.png",
        description: "现代复古变形镜，垂直椭圆虚化。",
        useCase: "风格化广告、类型片",
    },
];

export const FOCAL_LENGTHS = [14, 18, 24, 35, 40, 50, 65, 85, 100, 135, 200] as const;
export const APERTURES = [1.2, 1.4, 1.8, 2, 2.8, 4, 5.6, 8, 11, 16] as const;

export const FOCAL_LENGTH_META: Record<number, { zhName: string; description: string; useCase: string }> = {
    14: { zhName: "超广角", description: "14mm 极致广角，空间被极度拉伸", useCase: "建筑内景、风景全景、沉浸视角" },
    18: { zhName: "超广角", description: "18mm 超广角，强烈空间纵深", useCase: "街头摄影、室内、建筑" },
    24: { zhName: "广角", description: "24mm 广角，环境感强烈", useCase: "风光、大场面、新闻纪实" },
    35: { zhName: "小广角", description: "35mm 经典叙事焦段，接近人眼", useCase: "纪实、人物环境叙事" },
    40: { zhName: "准标准", description: "40mm 准标准，构图平衡", useCase: "人像叙事、文艺片" },
    50: { zhName: "标准", description: "50mm 标准，空间感自然", useCase: "人像、日常、街拍" },
    65: { zhName: "中焦", description: "65mm 中焦，轻微压缩，讨喜肤色", useCase: "半身人像、对话戏" },
    85: { zhName: "中长焦", description: "85mm 经典人像焦段", useCase: "特写人像、婚礼" },
    100: { zhName: "长焦", description: "100mm 长焦，背景压缩明显", useCase: "特写、微距、体育" },
    135: { zhName: "长焦", description: "135mm 电影专用长焦", useCase: "剧情特写、舞台" },
    200: { zhName: "超长焦", description: "200mm 超长焦，极致背景压缩", useCase: "体育、野生动物、戏剧感特写" },
};

export const APERTURE_META: Record<number, { zhName: string; description: string; useCase: string }> = {
    1.2: { zhName: "极大光圈", description: "f/1.2 虚化极致，景深刀削般薄", useCase: "梦幻人像、高光溢出效果" },
    1.4: { zhName: "大光圈", description: "f/1.4 强烈虚化，电影感十足", useCase: "夜景人像、暗光" },
    1.8: { zhName: "大光圈", description: "f/1.8 明显背景虚化", useCase: "半身人像、街头弱光" },
    2: { zhName: "大光圈", description: "f/2 柔和虚化与充足锐度", useCase: "人像、文艺日常" },
    2.8: { zhName: "标准大光圈", description: "f/2.8 景深适中、虚化柔和", useCase: "群像、叙事" },
    4: { zhName: "中光圈", description: "f/4 主体清晰，背景有轻微虚化", useCase: "群像、婚礼、电视剧" },
    5.6: { zhName: "中光圈", description: "f/5.6 背景可辨，环境感强", useCase: "纪实、日常、小品" },
    8: { zhName: "小光圈", description: "f/8 画质最佳区，前后皆清晰", useCase: "风光、建筑、全景" },
    11: { zhName: "小光圈", description: "f/11 超大景深，几乎全清晰", useCase: "大场景、产品摆拍" },
    16: { zhName: "极小光圈", description: "f/16 星芒出现，需高光源", useCase: "日光风光、星芒特效" },
};

const CAMERA_PROMPT_TEMPLATE = [
    "the following parameters describe the virtual camera, lens, and exposure used to render this image",
    "they are camera direction, NOT objects to add inside the frame",
    "do not add any physical camera, lens, tripod, viewfinder, or photography equipment into the image",
    "{{cameraProfilePrompt}}, {{lensProfilePrompt}}, {{focalLengthPrompt}}, {{aperturePrompt}}",
    "keep the subjects, scene, and action unchanged — apply these as optical and sensor characteristics only",
    "[camera body: {{cameraBody}} · lens: {{lens}} · focal: {{focalLengthMm}}mm · aperture: f/{{apertureF}}]",
].join(", ");

export function describeFocalLength(mm: number): string {
    if (mm <= 16) return `${mm}mm ultra-wide-angle perspective, strong spatial depth, pronounced edge distortion, immersive wide coverage`;
    if (mm <= 24) return `${mm}mm wide-angle perspective, exaggerated depth, strong foreground/background separation, environment-forward framing`;
    if (mm <= 35) return `${mm}mm slight-wide cinematic perspective, natural environmental context, balanced depth, classic story-telling framing`;
    if (mm <= 50) return `${mm}mm standard/normal perspective, close to natural human eye perspective, neutral spatial compression, flattering portrait framing`;
    if (mm <= 85) return `${mm}mm short-telephoto portrait perspective, mild background compression, flattering facial rendering, subject isolation`;
    if (mm <= 135) return `${mm}mm telephoto perspective, moderate compression, strong subject isolation from background, cinematic close-up`;
    return `${mm}mm long-telephoto perspective, strong background compression, highly isolated subject on compressed flat background, powerful dramatic framing`;
}

export function describeAperture(f: number): string {
    if (f <= 1.4) return `shot wide open at f/${f}, extremely shallow depth of field, strong creamy bokeh, razor-thin focus plane, dreamy background separation`;
    if (f <= 2) return `shot at f/${f}, very shallow depth of field, smooth cinematic bokeh, clear subject-from-background separation`;
    if (f <= 2.8) return `shot at f/${f}, shallow depth of field, soft bokeh, natural subject isolation`;
    if (f <= 4) return `shot at f/${f}, moderate depth of field, gentle falloff, clean background blur without over-smoothing`;
    if (f <= 5.6) return `shot at f/${f}, balanced depth of field, background retains context while subject remains clearly in focus`;
    if (f <= 8) return `shot at f/${f}, wide depth of field, environment and subject both sharp, documentary / landscape style`;
    return `shot at f/${f}, very wide depth of field, extensive sharpness from foreground to background, wide-open environmental feel`;
}

function applyTemplate(template: string, values: Record<string, string>): string {
    return template
        .replace(/\{\{(\w+)\}\}/g, (_, key: string) => values[key] ?? "")
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean)
        .join(", ");
}

export interface CameraPromptInput {
    cameraId: string;
    lensId: string;
    focalLengthMm: number;
    apertureF: number;
}

export function buildCameraPrompt(input: CameraPromptInput): string {
    // 镜头参数会进入图片生成写路径，未知枚举不能静默退回首项，否则持久化配置与实际生成语义会不一致。
    const camera = CAMERA_PROFILES.find((item) => item.id === input.cameraId);
    if (!camera) throw new Error(`不支持的相机配置：${input.cameraId}`);
    const lens = LENS_PROFILES.find((item) => item.id === input.lensId);
    if (!lens) throw new Error(`不支持的镜头配置：${input.lensId}`);
    if (!FOCAL_LENGTHS.includes(input.focalLengthMm as (typeof FOCAL_LENGTHS)[number])) throw new Error(`不支持的焦距：${input.focalLengthMm}mm`);
    if (!APERTURES.includes(input.apertureF as (typeof APERTURES)[number])) throw new Error(`不支持的光圈：f/${input.apertureF}`);
    return applyTemplate(CAMERA_PROMPT_TEMPLATE, {
        cameraProfilePrompt: camera.profilePrompt,
        lensProfilePrompt: lens.profilePrompt,
        focalLengthPrompt: describeFocalLength(input.focalLengthMm),
        aperturePrompt: describeAperture(input.apertureF),
        cameraBody: camera.shortTag,
        lens: lens.shortTag,
        focalLengthMm: String(input.focalLengthMm),
        apertureF: String(input.apertureF),
    });
}
