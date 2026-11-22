# 运镜

## 基本用法
`{"type":"camera_move","id":"<摄影机id>","technique":"push_in","time":0,"duration":4,"easing":"smooth","params":{"distance":2}}`
- 完整手法表见下。带 `*` 的手法必须给 `subjectId`，带 `°` 的可以给；大师手法配方见 [recipes.md](recipes.md)。
- 时间：`time` 是开始秒，`duration` 省略时到镜头结束；超过镜头时长会被拒绝。
- `easing`：linear 匀速、ease_in 加速、ease_out 减速、smooth 缓入缓出（默认）。
- 参数省略时会自动取值：推镜默认推近当前距离的 45%，跟拍按方位取距离与高度。

## 手法表
| technique | 名称 | subjectId | 参数 |
| --- | --- | --- | --- |
| push_in | 推镜头 | 不给 | distance 0.1–200 |
| pull_out | 拉镜头 | 不给 | distance 0.1–200 |
| dolly_zoom_out | 希区柯克变焦：后退同时推焦，主体不变背景压近 | 可选 | distance 0.1–200；aim 0–20 |
| dolly_zoom_in | 反向希区柯克：推近同时拉焦，背景拉开 | 可选 | distance 0.1–200；aim 0–20 |
| zoom_in | 变焦推（机位不动） | 不给 | focalLength 8–300 |
| zoom_out | 变焦拉（机位不动） | 不给 | focalLength 8–300 |
| pan_left | 左摇 | 不给 | angle 1–360 |
| pan_right | 右摇 | 不给 | angle 1–360 |
| tilt_up | 上摇 | 不给 | angle 1–80 |
| tilt_down | 下摇 | 不给 | angle 1–80 |
| whip_pan_left | 左甩镜 | 不给 | angle 15–270 |
| whip_pan_right | 右甩镜 | 不给 | angle 15–270 |
| truck_left | 左移 | 不给 | distance 0.1–200 |
| truck_right | 右移 | 不给 | distance 0.1–200 |
| roll | 旋转荷兰角（正数向右倾） | 不给 | angle -180–180 |
| pedestal_up | 平行升 | 不给 | distance 0.1–100 |
| pedestal_down | 平行降 | 不给 | distance 0.1–100 |
| crane_up | 摇臂升（镜头盯住焦点，distance=同时后退，即航拍拉升） | 不给 | height 0.2–100；distance 0–200 |
| crane_down | 摇臂降（distance=同时前推） | 不给 | height 0.2–100；distance 0–200 |
| orbit_left | 左环绕 | 可选 | angle 5–720；rise -50–50；aim 0–20 |
| orbit_right | 右环绕 | 可选 | angle 5–720；rise -50–50；aim 0–20 |
| follow | 跟拍（随演员移动，飞行/御剑时一起升空） | 必须 | side=behind/front/left/right/above/shoulder/pov；shotSize=extreme_close_up/close_up/medium/full/wide/extreme_wide；distance 0–60；height -5–100；lag 0–2；blend 0~10；aim 0–20 |
| look_at | 跟摇（机位不动盯住主体） | 必须 | aim 0–20 |
| handheld | 手持晃动（叠加层） | 不给 | intensity 0.1–3 |
| shake | 冲击震动（叠加层，默认 0.6 秒） | 不给 | intensity 0.1–3 |

## 叠加规则
- 多段按开始时间依次叠加：后一段从前一段结束时的机位继续。
- 时间重叠的段同时生效：环绕叠加升高就是螺旋升镜；跟拍叠加环绕就是绕着移动的人物转。
- `handheld`、`shake` 是叠加层，可以和任何运镜重叠。
- 运镜段叠加在摄影机的路径或关键帧之上：摄影机沿路径走，同时可以叠加 `look_at` 盯人、`handheld` 手持。

## 跟拍飞行的人物
人物先用 `actor_travel`（fly / sword_fly）或带高度的路径飞起来，再加 `follow`，机位会随人物一起升空。
```json
[
  {"type":"actor_travel","id":"hero","travelStyle":"sword_fly","time":0.5,"duration":4,"toMarker":"B","height":3},
  {"type":"camera_move","id":"cam","technique":"follow","subjectId":"hero","params":{"side":"behind","distance":6,"lag":0.35}},
  {"type":"camera_move","id":"cam","technique":"orbit_left","subjectId":"hero","time":2.5,"duration":2,"params":{"angle":120}}
]
```

## 跟拍景别与过渡
- `shotSize` 按当前焦段和画幅换算跟拍距离、注视高度与机位高度，优先于 `distance`。超近景贴近脸，中景保留半身动作，远景交代空间。
- 同一主体换 `side` 或 `shotSize` 时，后一段写 `blend`（秒，0–10），机位在指定时间内绕主体过渡；绕到另一侧通常 1.5–3 秒。
- 前一段跟拍要持续到后一段 `blend` 结束，避免交接期间机位停住、人物继续走远。
- 大师配方一次展开多段跟拍，见 [recipes.md](recipes.md) 与 `catalog.cameraRecipes`。

## 用户口语与手法
| 用户说 | 手法 |
| --- | --- |
| 慢慢推近、情绪推 | push_in，4–6 秒，smooth |
| 拉远、交代环境、结尾抽离 | pull_out 或 crane_up（distance=同时后退） |
| 镜头跟着人走 | follow side=behind / left / right |
| 绕着人转 | orbit_left / orbit_right，subjectId=人物 |
| 眩晕、背景压过来 | dolly_zoom_out，subjectId=人物 |
| 快速甩过去、转场 | whip_pan_left / whip_pan_right，0.4–0.6 秒 |
| 晃、紧张、纪实 | handheld，intensity 1–1.8 |
| 爆炸、撞击 | shake，0.4–0.8 秒 |
| 盯着某人（机位不动） | look_at |

## 删除与修改
- 删一段：`camera_move_remove` 加 `keyframeId=<运镜段 id>`，id 见 `cameras[].moves`。
- 清空全部运镜：`camera_move_remove` 不给 `keyframeId`。
- 改一段：`camera_move` 的 `keyframeId` 传原段 id，整段覆盖。
- 不要为了微调而叠加一段反向运镜去抵消，直接改或删原段。
- 时间轴里选中运镜段后按 Delete 应只删该段；保存重开后其他轨迹仍可播放。
