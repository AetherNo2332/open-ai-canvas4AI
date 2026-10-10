/**
 * Professional cinematography prompt library for the Camera Control panel.
 *
 * Ported from open-storyboard-canvas, simplified for the web project.
 * The output is visual direction for image and video models, not calibrated
 * optical simulation. Model names also draw on Radiance's camera catalog:
 * https://github.com/FXTD-Studios/radiance/blob/main/film/camera_profiles.py
 * the descriptions below are authored for this application.
 */

export interface CameraProfile {
    id: string;
    label: string;
    zhName: string;
    shortTag: string;
    profilePrompt: string;
    bodyColor: string;
    accentColor: string;
    useCase: string;
    description: string;
}

export interface LensProfile {
    id: string;
    label: string;
    zhName: string;
    shortTag: string;
    profilePrompt: string;
    lensColor: string;
    ringColor: string;
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

export const DEFAULT_CAMERA_CONTROL: CameraControlOptions = {
    enabled: false,
    camera: "arri_alexa_35",
    lens: "cooke_s7i",
    focalLength: 35,
    aperture: 2.8,
};

export const CAMERA_PROFILES: CameraProfile[] = [
    {
        id: "panavision_dxl2",
        label: "Panavision DXL2",
        zhName: "潘那维申 DXL2",
        shortTag: "Panavision DXL2",
        profilePrompt: "Panavision DXL2-inspired look, detailed tonal layers, soft bright-tone transitions, restrained saturation",
        bodyColor: "#2a2a2a",
        accentColor: "#e2a84f",
        description: "大画幅电影风格，强调层次与柔和的亮部过渡。",
        useCase: "剧情长片、奢华广告、顶级商业片",
    },
    {
        id: "arri_alexa_mini_lf",
        label: "ARRI Alexa Mini LF",
        zhName: "阿莱 Mini LF",
        shortTag: "ARRI Alexa Mini LF",
        profilePrompt: "ARRI Alexa Mini LF-inspired look, balanced skin tones, gentle highlight transitions, detailed shadow tones",
        bodyColor: "#3a3a3a",
        accentColor: "#d4d4d4",
        description: "大画幅风格，肤色平衡，明暗过渡柔和。",
        useCase: "剧情片、人像叙事、品牌广告",
    },
    {
        id: "red_komodo_6k",
        label: "RED Komodo 6K",
        zhName: "RED 科莫多 6K",
        shortTag: "RED Komodo 6K",
        profilePrompt: "RED Komodo 6K-inspired look, defined contours, fine surface detail, restrained shadow noise",
        bodyColor: "#8b2b2b",
        accentColor: "#ff3b3b",
        description: "紧凑电影机风格，强调轮廓与细节。",
        useCase: "动作片、FPV、穿越机、无人机",
    },
    {
        id: "red_v_raptor_8k",
        label: "RED V-Raptor 8K",
        zhName: "RED V-Raptor 8K",
        shortTag: "RED V-Raptor 8K",
        profilePrompt: "RED V-Raptor 8K VV-inspired look, precise texture, well-separated colors, clear shadow detail",
        bodyColor: "#701f1f",
        accentColor: "#ff5555",
        description: "大画幅数字风格，纹理清晰，色彩分离明确。",
        useCase: "科幻大片、高分辨率 VFX 场景",
    },
    {
        id: "sony_venice_2",
        label: "Sony Venice 2",
        zhName: "索尼 Venice 2",
        shortTag: "Sony Venice 2",
        profilePrompt: "Sony Venice 2-inspired look, neutral color balance, readable dark tones, controlled bright highlights",
        bodyColor: "#2d3a4d",
        accentColor: "#4d90d0",
        description: "全画幅电影风格，中性色彩，保留暗部层次。",
        useCase: "高端广告、纪录片、夜景戏剧",
    },
    {
        id: "sony_fx6",
        label: "Sony FX6",
        zhName: "索尼 FX6",
        shortTag: "Sony FX6",
        profilePrompt: "Sony FX6-inspired look, direct natural color, clear midtones, restrained digital texture",
        bodyColor: "#2d2d36",
        accentColor: "#ff9000",
        description: "自然直接的数字影像风格，适合纪实表达。",
        useCase: "纪录片、单兵采访、轻量剧情",
    },
    {
        id: "blackmagic_ursa_12k",
        label: "Blackmagic URSA Mini Pro 12K",
        zhName: "黑魔法 URSA Mini Pro 12K",
        shortTag: "Blackmagic URSA Mini Pro 12K",
        profilePrompt: "Blackmagic URSA Mini Pro 12K-inspired look, layered midtones, restrained contrast, fine texture",
        bodyColor: "#2e2e2e",
        accentColor: "#ffb820",
        description: "Super 35 数字电影风格，保留中间调和细节。",
        useCase: "独立电影、音乐短片、实验影像",
    },
    {
        id: "canon_c500_mk2",
        label: "Canon C500 Mk II",
        zhName: "佳能 C500 Mk II",
        shortTag: "Canon C500 Mk II",
        profilePrompt: "Canon C500 Mark II-inspired look, warm skin tones, gentle color separation, even tonal transitions",
        bodyColor: "#332a22",
        accentColor: "#e8c070",
        description: "全画幅电影风格，暖调肤色，色彩过渡平缓。",
        useCase: "广告、电视剧、婚礼电影",
    },
    {
        id: "arri_alexa_35",
        label: "ARRI Alexa 35",
        zhName: "阿莱 Alexa 35",
        shortTag: "ARRI Alexa 35",
        profilePrompt: "ARRI Alexa 35-inspired look, nuanced skin tones, soft highlight transitions, rich shadow separation",
        bodyColor: "#3a3a3a",
        accentColor: "#d4d4d4",
        description: "Super 35 电影风格，细腻肤色，亮部柔和，暗部有层次。",
        useCase: "剧情叙事、人物、品牌短片",
    },
    {
        id: "arri_alexa_65",
        label: "ARRI Alexa 65",
        zhName: "阿莱 Alexa 65",
        shortTag: "ARRI Alexa 65",
        profilePrompt: "ARRI Alexa 65-inspired look, fine texture across the frame, subtle tonal gradients, softly rendered highlights",
        bodyColor: "#3a3a3a",
        accentColor: "#d4d4d4",
        description: "大画幅电影风格，强调细密纹理与平滑色阶。",
        useCase: "景观、建筑、史诗题材",
    },
    {
        id: "arri_alexa_lf",
        label: "ARRI Alexa LF",
        zhName: "阿莱 Alexa LF",
        shortTag: "ARRI Alexa LF",
        profilePrompt: "ARRI Alexa LF-inspired look, natural midtone color, smooth bright-tone gradients, detailed shadows",
        bodyColor: "#3a3a3a",
        accentColor: "#d4d4d4",
        description: "大画幅数字电影风格，中间调自然，明暗衔接平缓。",
        useCase: "剧情长片、产品、场景叙事",
    },
    {
        id: "arri_amira",
        label: "ARRI Amira",
        zhName: "阿莱 Amira",
        shortTag: "ARRI Amira",
        profilePrompt: "ARRI Amira-inspired look, natural skin color, moderate contrast, readable highlight detail",
        bodyColor: "#3a3a3a",
        accentColor: "#d4d4d4",
        description: "自然纪实风格，肤色平实，反差适中。",
        useCase: "纪录片、采访、生活场景",
    },
    {
        id: "red_v_raptor_xl_8k",
        label: "RED V-Raptor XL 8K VV",
        zhName: "RED V-Raptor XL 8K",
        shortTag: "RED V-Raptor XL 8K VV",
        profilePrompt: "RED V-Raptor XL 8K VV-inspired look, detailed surfaces, clear color boundaries, dense shadow tones",
        bodyColor: "#701f1f",
        accentColor: "#ff5555",
        description: "大画幅数字风格，突出表面细节与色彩层次。",
        useCase: "科幻、产品、复杂场景",
    },
    {
        id: "red_monstro_8k",
        label: "RED Monstro 8K VV",
        zhName: "RED Monstro 8K",
        shortTag: "RED Monstro 8K VV",
        profilePrompt: "RED Monstro 8K VV-inspired look, finely resolved textures, saturated midtones, open shadow detail",
        bodyColor: "#701f1f",
        accentColor: "#ff5555",
        description: "大画幅风格，纹理细致，中间色饱满。",
        useCase: "风景、商业短片、视觉特效",
    },
    {
        id: "red_komodo_x",
        label: "RED Komodo-X",
        zhName: "RED Komodo-X",
        shortTag: "RED Komodo-X",
        profilePrompt: "RED Komodo-X-inspired look, defined edges, clean dark tones, precise color contrast",
        bodyColor: "#8b2b2b",
        accentColor: "#ff3b3b",
        description: "现代数字风格，轮廓清楚，暗调干净。",
        useCase: "动作、运动、城市题材",
    },
    {
        id: "red_helium_8k",
        label: "RED Helium 8K S35",
        zhName: "RED Helium 8K",
        shortTag: "RED Helium 8K S35",
        profilePrompt: "RED Helium 8K S35-inspired look, crisp fine detail, pronounced local contrast, distinct color layers",
        bodyColor: "#701f1f",
        accentColor: "#ff5555",
        description: "Super 35 数字风格，细节锐利，局部反差明确。",
        useCase: "产品、建筑、科技题材",
    },
    {
        id: "red_gemini_5k",
        label: "RED Gemini 5K S35",
        zhName: "RED Gemini 5K",
        shortTag: "RED Gemini 5K S35",
        profilePrompt: "RED Gemini 5K S35-inspired look, readable low-light detail, smooth dark gradients, restrained highlight contrast",
        bodyColor: "#701f1f",
        accentColor: "#ff5555",
        description: "弱光电影风格，保留暗部细节与渐变。",
        useCase: "夜景、室内、低照度叙事",
    },
    {
        id: "sony_venice_6k",
        label: "Sony Venice 6K",
        zhName: "索尼 Venice 6K",
        shortTag: "Sony Venice 6K",
        profilePrompt: "Sony Venice 6K-inspired look, balanced color, delicate skin-tone gradients, gradual highlight transitions",
        bodyColor: "#2d3a4d",
        accentColor: "#4d90d0",
        description: "全画幅电影风格，色彩均衡，肤色过渡细腻。",
        useCase: "剧情、广告、人物叙事",
    },
    {
        id: "sony_fx9",
        label: "Sony FX9",
        zhName: "索尼 FX9",
        shortTag: "Sony FX9",
        profilePrompt: "Sony FX9-inspired look, natural skin tones, clear tonal structure, restrained saturation",
        bodyColor: "#2d2d36",
        accentColor: "#ff9000",
        description: "全画幅纪实风格，肤色自然，色彩克制。",
        useCase: "纪录片、采访、纪实短片",
    },
    {
        id: "panavision_millennium_xl2",
        label: "Panavision Panaflex Millennium XL2",
        zhName: "潘那维申 Millennium XL2",
        shortTag: "Panavision Panaflex Millennium XL2",
        profilePrompt: "Panavision Panaflex Millennium XL2-inspired film look, subtle grain, dense midtones, gently softened bright areas",
        bodyColor: "#2a2a2a",
        accentColor: "#e2a84f",
        description: "35mm 胶片风格，细微颗粒与浓郁中间调。",
        useCase: "年代叙事、胶片风格剧情",
    },
    {
        id: "imax_mkiv",
        label: "IMAX MKIV 15/70mm",
        zhName: "IMAX MKIV 胶片机",
        shortTag: "IMAX MKIV 15/70mm",
        profilePrompt: "IMAX MKIV 15/70mm film-inspired look, fine grain, detailed textures, broad tonal separation",
        bodyColor: "#2a2a2a",
        accentColor: "#4d90d0",
        description: "大画幅胶片风格，细微颗粒与丰富层次。",
        useCase: "景观、自然、宏大场景",
    },
    {
        id: "blackmagic_cinema_6k",
        label: "Blackmagic Cinema Camera 6K",
        zhName: "黑魔法 Cinema Camera 6K",
        shortTag: "Blackmagic Cinema Camera 6K",
        profilePrompt: "Blackmagic Cinema Camera 6K-inspired look, natural midtones, gentle color contrast, detailed skin texture",
        bodyColor: "#2e2e2e",
        accentColor: "#ffb820",
        description: "全画幅数字风格，中间调自然，色彩反差柔和。",
        useCase: "独立短片、日常叙事、人像",
    },
    {
        id: "arricam_lt",
        label: "ARRICAM LT",
        zhName: "阿莱 ARRICAM LT",
        shortTag: "ARRICAM LT",
        profilePrompt: "ARRICAM LT-inspired film aesthetic, subtle grain, warm skin tones, soft highlight roll-off",
        bodyColor: "#252525",
        accentColor: "#c8c8c8",
        description: "胶片审美参考，轻微颗粒、温暖肤色与柔和亮部。",
        useCase: "剧情、年代感短片",
    },
    {
        id: "arriflex_435",
        label: "ARRIFLEX 435",
        zhName: "阿莱 ARRIFLEX 435",
        shortTag: "ARRIFLEX 435",
        profilePrompt: "ARRIFLEX 435-inspired film aesthetic, textured midtones, rich color separation, subtle grain",
        bodyColor: "#363636",
        accentColor: "#c4c4c4",
        description: "胶片质感参考，中间调有纹理，色彩层次丰富。",
        useCase: "广告、动作叙事、胶片风格",
    },
    {
        id: "imax_keighley",
        label: "IMAX Keighley",
        zhName: "IMAX Keighley",
        shortTag: "IMAX Keighley",
        profilePrompt: "IMAX Keighley-inspired large-format aesthetic, expansive visual detail, rich tonal layers, immersive scene scale",
        bodyColor: "#242424",
        accentColor: "#8ca0b5",
        description: "大画幅审美参考，强调场景细节与宽广空间层次。",
        useCase: "景观、史诗场景、环境叙事",
    },
    {
        id: "imax_film_camera",
        label: "IMAX Film Camera",
        zhName: "IMAX 胶片摄影机",
        shortTag: "IMAX Film Camera",
        profilePrompt: "IMAX film camera-inspired aesthetic, fine film grain, detailed textures, broad tonal separation",
        bodyColor: "#b1b1b1",
        accentColor: "#366b92",
        description: "大画幅胶片审美参考，细微颗粒与丰富明暗层次。",
        useCase: "自然、宏大场景、胶片风格",
    },
];

export const LENS_PROFILES: LensProfile[] = [
    {
        id: "arri_signature_prime",
        label: "ARRI Signature Prime",
        zhName: "阿莱 Signature 定焦",
        shortTag: "ARRI Signature Prime",
        profilePrompt: "ARRI Signature Prime-inspired rendering, rounded out-of-focus highlights, gentle tonal contrast, gradual focus transition",
        lensColor: "#2a2a2a",
        ringColor: "#c4a060",
        description: "圆润虚化、柔和反差，焦内外过渡平缓。",
        useCase: "人物叙事、顶级商业片",
    },
    {
        id: "cooke_s7i",
        label: "Cooke S7/i",
        zhName: "库克 S7/i",
        shortTag: "Cooke S7/i",
        profilePrompt: "Cooke S7/i-inspired rendering, warm midtones, gentle focus transition, rounded out-of-focus highlights",
        lensColor: "#26231d",
        ringColor: "#d0a060",
        description: "暖调中间色，焦内外过渡柔和，虚化圆润。",
        useCase: "古典人像、时代感叙事",
    },
    {
        id: "zeiss_supreme_prime",
        label: "Zeiss Supreme Prime",
        zhName: "蔡司至尊定焦",
        shortTag: "Zeiss Supreme Prime",
        profilePrompt: "Zeiss Supreme Prime-inspired rendering, neutral color, fine detail with moderate contrast, soft defocus transitions",
        lensColor: "#1e1e22",
        ringColor: "#4d90d0",
        description: "中性色彩，细节明确，虚化过渡平滑。",
        useCase: "科技感广告、现代剧情",
    },
    {
        id: "canon_sumire_prime",
        label: "Canon Sumire Prime",
        zhName: "佳能 Sumire 定焦",
        shortTag: "Canon Sumire Prime",
        profilePrompt: "Canon Sumire Prime-inspired rendering, warm tones, softened fine contrast, rounded bokeh",
        lensColor: "#2c241c",
        ringColor: "#e0b070",
        description: "柔和梦幻肤色镜，暖调柔焦。",
        useCase: "爱情片、梦境、人物特写",
    },
    {
        id: "anamorphic_cooke",
        label: "Cooke Anamorphic /i",
        zhName: "库克 Anamorphic /i",
        shortTag: "Cooke Anamorphic /i",
        profilePrompt: "Cooke Anamorphic /i-inspired rendering, oval out-of-focus highlights, warm color, gentle focus transition",
        lensColor: "#1a1a24",
        ringColor: "#6a9ed6",
        description: "变形镜头风格，椭圆虚化与温暖色调。",
        useCase: "科幻大片、太空戏、夜戏",
    },
    {
        id: "anamorphic_atlas",
        label: "Anamorphic (Atlas)",
        zhName: "Atlas Orion 变形",
        shortTag: "Atlas Orion Anamorphic",
        profilePrompt: "Atlas Orion anamorphic-inspired rendering, oval bokeh, horizontal flare response around existing bright sources, softened frame edges",
        lensColor: "#1b2228",
        ringColor: "#40c0e0",
        description: "现代变形镜，青色光晕，中心清晰边缘柔。",
        useCase: "音乐 MV、风格化广告",
    },
    {
        id: "vintage_leica_r",
        label: "Vintage Leica R",
        zhName: "徕卡 R 老镜",
        shortTag: "Vintage Leica R",
        profilePrompt: "vintage Leica R-inspired rendering, deep color, mild glow around existing highlights, softer edge detail",
        lensColor: "#3a2f1f",
        ringColor: "#d04040",
        description: "上世纪老镜，高光微透溢，复古怀旧味。",
        useCase: "年代戏、怀旧 MV、文艺片",
    },
    {
        id: "macro_100mm",
        label: "Macro 100mm",
        zhName: "100mm 微距",
        shortTag: "Macro 100mm",
        profilePrompt: "macro lens-inspired rendering, detailed focus plane, smooth out-of-focus texture",
        lensColor: "#1f1f1f",
        ringColor: "#60d090",
        description: "微距特写专用，细节惊人，景深极浅。",
        useCase: "美食广告、珠宝、昆虫、细节",
    },
    {
        id: "arri_master_prime",
        label: "ARRI Master Prime",
        zhName: "阿莱 Master 定焦",
        shortTag: "ARRI Master Prime",
        profilePrompt: "ARRI Master Prime-inspired rendering, precise in-focus detail, defined contrast, clear color separation",
        lensColor: "#2a2a2a",
        ringColor: "#c4a060",
        description: "焦内细节清楚，反差明确，色彩分离清晰。",
        useCase: "产品、商业剧情、细节表现",
    },
    {
        id: "arri_ultra_prime",
        label: "ARRI Ultra Prime",
        zhName: "阿莱 Ultra 定焦",
        shortTag: "ARRI Ultra Prime",
        profilePrompt: "ARRI Ultra Prime-inspired rendering, balanced edge detail, clear tonal contrast, consistent neutral color",
        lensColor: "#2a2a2a",
        ringColor: "#c4a060",
        description: "细节与反差均衡，色彩偏中性。",
        useCase: "纪实、剧情、场景叙事",
    },
    {
        id: "arri_master_anamorphic",
        label: "ARRI Master Anamorphic",
        zhName: "阿莱 Master 变形",
        shortTag: "ARRI Master Anamorphic",
        profilePrompt: "ARRI Master Anamorphic-inspired rendering, oval defocused highlights, defined central detail, restrained edge stretching",
        lensColor: "#2a2a2a",
        ringColor: "#c4a060",
        description: "椭圆虚化，中心细节明确，边缘拉伸克制。",
        useCase: "剧情长片、夜景、风格化短片",
    },
    {
        id: "cooke_panchro_i_classic",
        label: "Cooke Panchro/i Classic",
        zhName: "库克 Panchro/i Classic",
        shortTag: "Cooke Panchro/i Classic",
        profilePrompt: "Cooke Panchro/i Classic-inspired vintage rendering, warm midtones, softened fine contrast, rounded defocused highlights",
        lensColor: "#26231d",
        ringColor: "#d0a060",
        description: "复古暖调，细节反差柔和，失焦高光圆润。",
        useCase: "年代戏、人物、怀旧短片",
    },
    {
        id: "zeiss_cp3",
        label: "Zeiss CP.3 Compact Prime",
        zhName: "蔡司 CP.3 紧凑定焦",
        shortTag: "Zeiss CP.3 Compact Prime",
        profilePrompt: "Zeiss CP.3-inspired rendering, neutral color balance, clean contours, even contrast",
        lensColor: "#1e1e22",
        ringColor: "#4d90d0",
        description: "色彩中性，轮廓清楚，反差均匀。",
        useCase: "纪实、商业短片、产品",
    },
    {
        id: "panavision_primo_70",
        label: "Panavision Primo 70",
        zhName: "潘那维申 Primo 70",
        shortTag: "Panavision Primo 70",
        profilePrompt: "Panavision Primo 70-inspired rendering, finely separated textures, rounded tonal transitions, clear focus plane",
        lensColor: "#2a2a2a",
        ringColor: "#e2a84f",
        description: "大画幅镜头风格，纹理清楚，色阶衔接圆润。",
        useCase: "场景叙事、风景、商业剧情",
    },
    {
        id: "panavision_c_series",
        label: "Panavision C-Series Anamorphic",
        zhName: "潘那维申 C 系列变形",
        shortTag: "Panavision C-Series Anamorphic",
        profilePrompt: "Panavision C-Series-inspired vintage anamorphic rendering, oval bokeh, softer edges, blue horizontal flares around existing bright sources",
        lensColor: "#2a2a2a",
        ringColor: "#e2a84f",
        description: "复古变形风格，椭圆虚化与柔和边缘。",
        useCase: "年代叙事、夜景、复古科幻",
    },
    {
        id: "panavision_e_series",
        label: "Panavision E-Series Anamorphic",
        zhName: "潘那维申 E 系列变形",
        shortTag: "Panavision E-Series Anamorphic",
        profilePrompt: "Panavision E-Series-inspired anamorphic rendering, oval defocus, defined subject detail, gradual edge softness",
        lensColor: "#2a2a2a",
        ringColor: "#e2a84f",
        description: "变形镜头风格，主体细节清楚，边缘柔和。",
        useCase: "剧情、广告、夜景短片",
    },
    {
        id: "leica_summilux_c",
        label: "Leica Summilux-C",
        zhName: "徕卡 Summilux-C",
        shortTag: "Leica Summilux-C",
        profilePrompt: "Leica Summilux-C-inspired rendering, delicate in-focus texture, gentle color gradients, soft defocused backgrounds",
        lensColor: "#3a2f1f",
        ringColor: "#d04040",
        description: "焦内纹理细腻，色阶柔和，虚化平滑。",
        useCase: "人物、剧情、精细产品",
    },
    {
        id: "leica_thalia",
        label: "Leica Thalia",
        zhName: "徕卡 Thalia",
        shortTag: "Leica Thalia",
        profilePrompt: "Leica Thalia-inspired rendering, rounded tonal depth, delicate texture, gradual focus falloff",
        lensColor: "#3a2f1f",
        ringColor: "#d04040",
        description: "大画幅镜头风格，层次圆润，焦点过渡舒缓。",
        useCase: "人物叙事、风景、艺术短片",
    },
    {
        id: "canon_k35",
        label: "Canon K-35",
        zhName: "佳能 K-35 复古定焦",
        shortTag: "Canon K-35",
        profilePrompt: "Canon K-35-inspired vintage rendering, reduced fine contrast, warm colors, mild glow around existing highlights",
        lensColor: "#2c241c",
        ringColor: "#e0b070",
        description: "复古低反差，温暖色调，高光轻微晕开。",
        useCase: "年代片、梦境、复古人像",
    },
    {
        id: "angenieux_optimo_ultra_12x",
        label: "Angenieux Optimo Ultra 12x",
        zhName: "安琴 Optimo Ultra 12x",
        shortTag: "Angenieux Optimo Ultra 12x",
        profilePrompt: "Angenieux Optimo Ultra 12x-inspired rendering, balanced color separation, even contrast, smoothly defocused detail",
        lensColor: "#26231d",
        ringColor: "#d0a060",
        description: "电影变焦镜头风格，反差均衡，虚化平滑。",
        useCase: "剧情、舞台、商业短片",
    },
    {
        id: "angenieux_ez",
        label: "Angenieux EZ Series",
        zhName: "安琴 EZ 系列",
        shortTag: "Angenieux EZ Series",
        profilePrompt: "Angenieux EZ Series-inspired rendering, clear focus-plane detail, natural midtones, moderate contrast",
        lensColor: "#26231d",
        ringColor: "#d0a060",
        description: "现代变焦镜头风格，焦内清楚，中间调自然。",
        useCase: "纪实、轻量剧情、日常短片",
    },
    {
        id: "cooke_s4",
        label: "Cooke S4/i",
        zhName: "库克 S4/i",
        shortTag: "Cooke S4/i",
        profilePrompt: "Cooke S4/i-inspired rendering, warm midtones, smooth focus transition, gentle contrast",
        lensColor: "#232323",
        ringColor: "#d8b452",
        description: "暖调中间色与平缓的焦内外过渡。",
        useCase: "人物剧情、温暖室内场景",
    },
    {
        id: "cooke_speed_panchro",
        label: "Cooke Speed Panchro",
        zhName: "库克 Speed Panchro",
        shortTag: "Cooke Speed Panchro",
        profilePrompt: "Cooke Speed Panchro-inspired vintage rendering, softened fine contrast, warm color, gentle highlight glow",
        lensColor: "#36312b",
        ringColor: "#c3a37b",
        description: "复古暖调，细节反差柔和，亮部轻微光晕。",
        useCase: "年代叙事、复古人像",
    },
    {
        id: "cooke_sf_1_8x",
        label: "Cooke SF 1.8x",
        zhName: "库克 SF 1.8x",
        shortTag: "Cooke SF 1.8x",
        profilePrompt: "Cooke SF 1.8x anamorphic-inspired rendering, oval bokeh, expressive horizontal flare, warm tonal transitions",
        lensColor: "#202020",
        ringColor: "#d4bd43",
        description: "变形镜头审美参考，椭圆虚化与横向眩光。",
        useCase: "夜景、剧情长片、风格化短片",
    },
    {
        id: "helios_vintage",
        label: "Helios",
        zhName: "Helios 复古镜头",
        shortTag: "Helios vintage prime",
        profilePrompt: "Helios-inspired vintage rendering, swirling background bokeh, soft peripheral detail, expressive focus separation",
        lensColor: "#323232",
        ringColor: "#b4b4b4",
        description: "复古镜头审美参考，旋转背景虚化与柔和边缘。",
        useCase: "梦幻人像、复古街景",
    },
    {
        id: "panavision_primo",
        label: "Panavision Primo",
        zhName: "潘纳维申 Primo",
        shortTag: "Panavision Primo",
        profilePrompt: "Panavision Primo-inspired rendering, balanced contrast, natural skin tones, smooth background separation",
        lensColor: "#242424",
        ringColor: "#e6e6e6",
        description: "均衡反差、自然肤色与平滑背景过渡。",
        useCase: "人物叙事、商业广告",
    },
    {
        id: "hawk_class_x",
        label: "Hawk class-X",
        zhName: "Hawk class-X",
        shortTag: "Hawk class-X",
        profilePrompt: "Hawk class-X anamorphic-inspired rendering, oval defocused highlights, expressive edge softness, cinematic horizontal flare",
        lensColor: "#222222",
        ringColor: "#9b9b9b",
        description: "变形镜头审美参考，椭圆高光、柔和边缘与横向眩光。",
        useCase: "剧情、夜景、音乐短片",
    },
];

export const FOCAL_LENGTHS = [14, 18, 24, 35, 40, 50, 65, 85, 100, 135, 200] as const;
export const APERTURES = [1.2, 1.4, 1.8, 2, 2.8, 4, 5.6, 8, 11, 16] as const;

export const FOCAL_LENGTH_META: Record<number, { zhName: string; description: string; useCase: string }> = {
    14: { zhName: "超广角", description: "14mm 极致广角，空间被极度拉伸", useCase: "建筑内景、风景全景、沉浸视角" },
    18: { zhName: "超广角", description: "18mm 超广角，强烈空间纵深", useCase: "街头摄影、室内、建筑" },
    24: { zhName: "广角", description: "24mm 广角，环境感强烈", useCase: "风光、大场面、新闻纪实" },
    35: { zhName: "小广角", description: "35mm 叙事视角，兼顾主体与环境", useCase: "纪实、人物环境叙事" },
    40: { zhName: "准标准", description: "40mm 准标准，构图平衡", useCase: "人像叙事、文艺片" },
    50: { zhName: "标准", description: "50mm 标准，空间感自然", useCase: "人像、日常、街拍" },
    65: { zhName: "中焦", description: "65mm 中焦风格，轻微空间压缩感", useCase: "半身人像、对话戏" },
    85: { zhName: "中长焦", description: "85mm 经典人像焦段", useCase: "特写人像、婚礼" },
    100: { zhName: "长焦", description: "100mm 长焦，背景压缩明显", useCase: "特写、微距、体育" },
    135: { zhName: "长焦", description: "135mm 长焦风格，突出空间压缩感", useCase: "剧情特写、舞台" },
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
    8: { zhName: "小光圈", description: "f/8 作为较大景深的视觉参考", useCase: "风光、建筑、全景" },
    11: { zhName: "小光圈", description: "f/11 强调环境细节与较深景深", useCase: "大场景、产品摆拍" },
    16: { zhName: "极小光圈", description: "f/16 深景深风格，保留前后景层次", useCase: "日光风光、环境叙事" },
};

const CAMERA_PROMPT_TEMPLATE = [
    "Camera visual direction: use the following choices as an aesthetic reference for color, framing, and depth of field",
    "{{cameraProfilePrompt}}, {{lensProfilePrompt}}, {{focalLengthPrompt}}, {{aperturePrompt}}",
    "preserve the requested subjects, scene, action, and aspect ratio",
    "[camera body: {{cameraBody}} · lens: {{lens}} · focal: {{focalLengthMm}}mm · aperture: f/{{apertureF}}]",
].join(", ");

export function describeFocalLength(mm: number): string {
    if (mm <= 16) return `${mm}mm ultra-wide-angle look, expansive field of view, pronounced foreground depth`;
    if (mm <= 24) return `${mm}mm wide-angle look, visible environmental context, emphasized near-to-far scale`;
    if (mm <= 35) return `${mm}mm slight-wide look, balanced subject and environmental context`;
    if (mm <= 50) return `${mm}mm standard-lens look, balanced spatial proportions, restrained perspective emphasis`;
    if (mm <= 85) return `${mm}mm short-telephoto look, mild background compression, stronger subject separation`;
    if (mm <= 135) return `${mm}mm telephoto look, layered compressed space, narrower field of view`;
    return `${mm}mm long-telephoto look, pronounced background compression, narrow field of view`;
}

export function describeAperture(f: number): string {
    if (f <= 1.4) return `f/${f} aperture reference, very shallow depth of field, narrow focus plane, strongly softened background`;
    if (f <= 2) return `f/${f} aperture reference, shallow depth of field, smooth bokeh, clear focus separation`;
    if (f <= 2.8) return `f/${f} aperture reference, shallow depth of field, soft defocus, gentle subject separation`;
    if (f <= 4) return `f/${f} aperture reference, moderate depth of field, gentle background blur`;
    if (f <= 5.6) return `f/${f} aperture reference, balanced depth of field, readable background context`;
    if (f <= 8) return `f/${f} aperture reference, deeper depth of field, retain subject and environmental detail`;
    return `f/${f} aperture reference, deep depth of field, retain foreground and background detail`;
}

function applyTemplate(template: string, values: Record<string, string>): string {
    return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) => values[key] ?? "")
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
    // 镜头参数进入生成写路径；未知枚举不能静默退回首项，避免实际生成与持久化配置不一致。
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

export function buildCameraControlPrompt(options?: CameraControlOptions): string {
    if (!options?.enabled) return "";
    return buildCameraPrompt({
        cameraId: options.camera,
        lensId: options.lens,
        focalLengthMm: options.focalLength,
        apertureF: options.aperture,
    });
}

export function appendCameraControlPrompt(prompt: string, options: CameraControlOptions | undefined, mode: string): string {
    if (mode !== "image" && mode !== "video") return prompt;
    const direction = buildCameraControlPrompt(options);
    if (!direction) return prompt;
    return prompt ? `${prompt}\n\n${direction}` : direction;
}
