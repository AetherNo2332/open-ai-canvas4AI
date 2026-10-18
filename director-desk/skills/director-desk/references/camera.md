# 镜头与灯光

这些字段可选；省略保持旧工程的默认照明和镜头，不自动改动已有路径或跟踪。设计运镜时按剧情安排路线、经过时刻和停顿，不要求套用预设。`camera.effects`、实体 `light`、工程 `lighting` 作为嵌套对象整体替换，先读取原值并保留无关参数。

- **运镜预设**：`{operation:"camera-motion",id:"机位ID",asset:"arc-push",time:0,duration:5,patch:{amplitude:2,angle:70,side:1,easing:"smooth"}}`。预设有 push、pull、truck、rise、descend、arc、arc-push、crane-reveal、ground-rise、whip-pan、push-pause、dolly-zoom、roll-recover、reframe。路径预设替换整条机位路径；倾斜、构图、甩镜改对应通道。POV 先改为独立机位才能应用路径预设。
- **连续运动**：位置 `path` 和视线 `camera.targetPath` 共用 `{smooth:false,interpolation:"continuous",points:[{time:0,position:[0,1,0]},{time:2,position:[1,1,0]},{time:5,position:[3,1,-2],stop:true}]}`。按实际点时间做速度与加速度连续的时间插值；中间点默认经过，`stop:true` 在该点停住，首尾默认停止，显式 `stop:false` 可让端点经过。相邻两点位置相同表示停留。每点仍需严格递增时间；点位和时间决定实际速度，弯曲路径可略偏离点间直线。continuous 不使用 smooth，且 easing 只能省略或为 linear；切换时显式移除旧分段 easing，UI 切换也会清除它们。
- **原有速度曲线**：省略 interpolation 保持旧求值方式。途经点 `easing` 控制从前一点到该点的进度：linear（省略时默认）、smooth、ease-in、ease-out、hold、whip。位置路径 `smooth` 只控制空间形状；视线 targetPath 的 smooth 在未指定 easing 时给每段缓入缓出。逐段 smooth 会逐点停住。hold 是到时跳变，普通停留用相同位置的两个点。
- **镜头通道**：`camera.effects.channels` 可含 focal（8–300 mm）、roll/pan/tilt（度）、offsetX/Y/Z（机身局部米）、frameX/Y（−0.9–0.9，中心 0，左右三分位 ±0.333，正值右／上）、focusDistance（0.05–2000 m）、blur/distortion（0–1）、bloom（0–2）。每个值为常数，或 `{keys:[{time:0,value:28},{time:5,value:70,easing:"smooth"}]}`；时间严格递增，端点外保持。
- 连贯插值保证源路径关键点之间的速度与加速度连续；时间轴片段若使用不同播放倍率或插入空档，拼接边界仍可能变速。停点指定的是瞬时停住，停留一段时间需重复坐标。
- **视线响应**：`camera.aimResponse:{duration:0.35}`，duration 为 0—2 秒；用于非 POV 的目标瞄准，free+manual／POV 旁路；follow 仍看向目标。省略或 0 保持原有精确锁定；与 `effects.followLag` 的位置延迟独立。按固定采样确定性求值，无需播放历史；手持绑定或场域动画驱动目标的历史姿态只作近似。离线同样保存该字段，画面由软件求值。
- **镜头设置**：effects 可加 `distortionType:"barrel"|"pincushion"|"fisheye"`、`focusTargetId`（空串改用对焦距离）、`followLag`（跟随机位位置延迟，0–5 秒）、`shake:{preset:"breath"|"walk"|"run"|"impact"|"pov",amount:1,frequency:1,seed:1,start:0,end:5}`。晃动强度 0–5，频率倍率 0.1–10；null 关闭。`dollyZoom:{distance:参考距离米,focal:参考焦距mm}` 优先于焦距通道，距离变化时反向配合焦距，超出 8–300 mm 会钳制；null 关闭。
- **环境预设**：`{operation:"lighting-preset",asset:"dusk"}`；可选 daylight、dusk、moonlight、interior-warm、interior-cool、silhouette。只替换全局环境，保留用户独立灯具。
- **独立灯具**：add 的 asset 可用 light-point、light-spot、light-area、light-sun，kind 省略。沿用实体 position、rotation、path、color；光朝局部 −Z。默认参数随资产生成，读取后可改 `light:{intensity:100,range:20,angle:40,penumbra:0.3,width:3,height:2,shadows:true}`。面光需要 shadows:false。可选 throughWalls:boolean 默认 false；开启后仅忽略内置墙顶和建筑外壳对该灯的挡光，保留人物、家具阴影。普通几何体和导入模型不自动归为墙体。intensity 为 0–100000；可为动画值。可加 temperature（1000–15000 K 的动画值）、colorKeys（`[{time,color,easing?}]`）、flicker（`{strength:0.3,frequency:4,seed:1,start:0}`）。灯具标记不进参考视频，实际照明会进入。
- **场景环境**：`lighting:{defaultLights:true,ambient:2.5,exposure:1.05,background:"#c6c8c6",groundColor:"#88847e",quality:"medium"}`；quality 为 off/low/medium/high。ambient（0–20）、exposure（0.05–10）支持动画值；可加 sunColor、sunIntensity（0–100）、sunDirection（非零 XYZ 向量），以及 `fog:{color:"#cccccc",density:0.02}`（density 0–0.5，可动画）。设 defaultLights:false 可只用环境光与自建灯具。

畸变后的空间入画范围是包围边界采样估计，遮挡射线用同一畸变的逆投影；景深模糊不算几何遮挡。离线校验只检查格式和引用，不能代替实际渲染。灯光和镜头参数随戏段独立保存，接拍冻结实际末帧，不重播前段效果动画。

## 独立视线路径

摄影机可写 `patch.camera.targetPath:{smooth:true,points:[{time:0,position:[0,1,0]},{time:5,position:[3,1,-2]}]}`，时间严格递增；该字段独立于摄影机位置 path。省略 interpolation 时，smooth 控制每段注视点的缓入缓出，会在途经点停顿；continuous 按实际点时间做速度与加速度连续的时间插值，smooth 不再起作用。首帧前与末帧后保持端点。独立机位 aim:"target" 和 follow 优先使用该视线，follow 的 targetId 仍用于位置跟随；free 手动旋转和 POV 不使用它。坐标是静态世界注视点，不是持续绑定人物；设 targetPath:null 恢复普通目标规则。按需使用，不要求每台摄影机配置。

## 自定义速度曲线与修改定位

关键帧和原有分段路径点的 `easing` 除现有预设名外，也可为 `{bezier:[x1,y1,x2,y2]}`，四个控制值均为 0—1，横轴为归一化时间，纵轴为归一化进度；控制相邻前一帧到此帧的插值，不改变空间路线。`interpolation:"continuous"` 的路径／视线不叠加此缓动；通过点时间和 stop 设计速度与停顿。UI 选中对象的「曲线」可拖动控制柄。真正停留用同位置关键帧或恒定参数；hold 是保持后跳变。UI「起点停留 1 秒」复制区间起点值，并顺延本通道后续关键帧，不移动其他轨道；带时间重排片段的路径需在时间轴安排停留。


## 深度画面与视频

- 可选 `depthVideo.curve` 为 0.25—4，省略或 1 保持线性。大于 1 加强近处表面的灰度层次，小于 1 加强远处；先做曲线再黑白反转。曲线和近远范围在整段固定，旧工程不自动改变。
- `director_spatial({depthRange:true,time,cameraId,ids?:[entityId]})` 仅返回当前时刻的建议 `depthRange:{near,far,samples}`，不修改工程。省略 ids 取当前机位画面；提供 ids 时仅采样这些对象在画幅内的可见表面，暂时排除其他对象与房间，因此不用于遮挡检查。依据低分辨率几何采样的 2%—98% 深度分位并留边距，不是整段保证；空画面报错。包含镜头畸变，局部空间扭曲尚不参与取值，正常深度输出仍保留扭曲。
- UI 的“取选中范围／取画面范围”使用同一取值能力，一次写入固定近远值。取选中范围适合突出主体，范围外背景可能压黑或压白；人物大幅走位或切镜时按需要扩大范围。AI 应保留 enabled、invert、curve 等未改字段，通过原 project 补丁应用；离线工具支持保存与校验 curve，但不执行 GPU 取值。

直接读取三维表面的摄影机空间深度，不需要外部模型或服务。普通画面与深度共用摄影机、画幅、时间、切镜、动画、变焦和畸变。

- 按需读取 `director_read({sections:["scene"]})` 的 `depthVideo`。
- `project` 操作可写 `patch.depthVideo={enabled:true,near:0.1,far:30,invert:false}`；整个对象替换。`near`、`far` 单位为米，`0 <= near < far <= 2000`。范围在整场内固定，近亮远暗，`invert` 反转；`enabled` 控制摄影机预览及截图，按戏段保存，可撤销。
- `director_export({kind:"depth-video"})` 使用当前戏段的深度范围导出 MP4，可选 `start/end/size`，沿用工程帧率、画幅和切镜。无需先开启预览。未设置范围时为 0.1—30 米。`kind:"video"` 始终输出普通画面，不受深度预览开关影响。
- 深度不含灯光、阴影、颜色、景深模糊、泛光和人物标签。透明、镜面和媒体按实际承载几何表面当作不透明表面输出，不解释反射或贴图中的物体；几何形变仍生效。
- 输出是供参考的灰度视频，受视频压缩及灰度精度影响，不是无损米制深度数据。不得把它表述为外部视频模型一定支持的控制输入。
- 离线工程使用同一 `depthVideo` 字段；视频渲染仍需在导演台中执行。
