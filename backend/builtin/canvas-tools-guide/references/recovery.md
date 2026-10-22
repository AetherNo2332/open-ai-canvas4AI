# 错误修正：从回执决定下一步

不要复发同一失败调用；先读 reason、errorClass、field、issue、argumentHints 或 guidance（以实际回执为准）。回执不重复完整参数 schema；本轮工具契约仍是参数权威。exampleArguments 是最小形状示例，必须替换占位符并保留原意，不能照抄成另一个操作。

| 回执或现象 | 下一步 |
| --- | --- |
| schema/参数错误 | 对照本轮 schema 修正具体字段、类型、枚举和 action；只重试已修正且仍在用户范围内的调用 |
| JSON 截断、参数不完整 | 确认未执行后减少单次 payload，按实际大小拆必要写入；不要丢用户要求的内容 |
| 快照过期或冲突 | 重读目标和结构化行，重新判断用户修改后是否仍适用；使用新 hash 重新提交，待审批内容变化时遵守重新审批流程 |
| 节点/行/列不存在 | 精读实际节点、行或列；目标确已删除就停下说明，不按旧 ID 猜补一个 |
| invalidRowIds / duplicateRowIds | 按完整列表修正本批行选择；读写都拒绝不存在或重复的行，写入整批不生效 |
| 引用字段错误或容量不足 | 按回执的 field/issue 检查 nodeId、role、order、优先级与实际容量；不擅自清空用户已有输入、不改 metadata 绕过校验 |
| connection_ops_disabled | 连线操作已停用：改用 `generationSpec.referenceBindings`、`metadata.inputBindings`、行 `assetBindings` 或 `set_references`／`bind_output`／`unbind_output`，不要重试旧的边操作 |
| 引用重复 | 核对真实 nodeId 与角色；已满足目标则无需再写，不能靠换 ID 重复声明 |
| 权限不足、锁定、只读、审批拒绝、预算不足 | 说明具体阻碍；不能换工具绕过或擅自升级权限、预算和模式 |
| 待审批 | 按回执等待授权，不当成功，不重复申请相同动作 |
| 写入后断流/超时 | 重读目标确认是否落地；若已成功直接继续，状态未知先报告，不重复创建 |
| 已有生成 taskId 或任务失败 | 查询该 taskId 和目标节点，不自动重新收费提交 |
| finish_run 返回 completionBlocked | 按真实结果对账计划、继续必要工作或用 ask_user 请求确实缺失的信息，不循环申请收尾 |

`canvas_apply_ops` 失败时整批不落地（包括混有旧连线操作的批次，合法改名也不会生效）。修正前核对画布，不能假定其中前几项已经成功。拆批时要重新获取快照并保持节点创建、引用写入等依赖顺序。

同一问题修正后仍失败且没有新证据，就停止重复调用，给用户明确的工具、原因与尚未完成的操作。不要为消除错误扩展任务范围。

读取回执包含 `readCallsUsed/readCallsLimit/readCallsRemaining`，是本次读取后的共享配额；缓存重放不应凭旧回执推算当前剩余量，使用本次回执。原生技能 `read` 也报告该配额，同一步同页重试不重复扣减。接近上限时优先最小必要读取；额度耗尽不能换参数或工具绕过。
