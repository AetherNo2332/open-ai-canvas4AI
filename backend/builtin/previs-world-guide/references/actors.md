# 摆人物与调度

## 加人物
`object_add kind=actor primitive=character archetype=adult|man|woman|child|elderly|monster name=… position=[x,0,z] rotation=[0,yaw,0] pose=stand|sit|…`
- 绑定画布角色卡：`characterAssetId`、`characterVersionId`、`referenceNodeId` 三个都要，并且必须与 `canvas_get_state` 返回的角色卡完全一致。普通图片里的人不是角色卡，不要传这三个字段。
- 每个人给不同颜色（`color`），生成视频时颜色就是身份标记。
- `position[1]` 是脚底离地高度。站上台阶、桌面、平台时传入台面高度，例如 `0.8`；不要把它固定为 0。

## 朝向
- yaw = atan2(目标x − 自己x, 目标z − 自己z)。面朝 +Z 是 0，面朝 −Z 是 3.1416，面朝 +X 是 1.5708。
- 两人对话：先算出彼此的朝向，再微调 ±0.3 弧度，避免完全正对着镜头。

## 站位与轴线
- 两人对话间距 1.0–1.8 米；争吵 0.6–1.0 米；陌生人 2 米以上。
- 对话场景的机位留在两人连线的同一侧（180° 轴线），否则正反打时人物会互换左右。
- 群戏：前景 1–2 人、中景主体、背景群众，用 `object_array_add` 或布景的 `crowd`。

## 动作
- 当前 4AI 接入以姿势和位移为主：`object_update pose=stand|sit|walk|run|fight|kick|…`。
- 走路和跑步的步态由位移速度自动推导；A→B 用 `actor_travel` 或 `object_motion_path_set`，不要用原地姿势代替位移。

## 范例：客厅两人对峙
```json
[
  {"type":"object_add","id":"a","kind":"actor","archetype":"man","name":"男主","position":[-0.6,0,0],"rotation":[0,1.4,0],"color":"#2f7de1"},
  {"type":"object_add","id":"b","kind":"actor","archetype":"woman","name":"女主","position":[0.6,0,0.1],"rotation":[0,-1.7,0],"color":"#d84949"}
]
```
