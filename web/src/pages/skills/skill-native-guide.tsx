import { App, Button } from "antd";
import { Copy } from "lucide-react";
import copy from "copy-to-clipboard";

import { AppModal } from "@/components/ui/product/app-modal";

import "./skill-native-guide.css";

const conversionPrompt = `请把我提供的技能转换为“影策云端 Agent”可用的 native skill。

先检查原包的 SKILL.md、references、scripts 和工具依赖，在新目录输出适配版，保留原包。不要执行来源包的安装器或脚本，也不要把技能安装到我本机的 Codex 配置目录。

目标格式：
1. 每个技能保留自己的目录与 UTF-8 编码的 SKILL.md；合集可以包含多个技能目录。用 YAML frontmatter 提供 name 和 description；name 使用简短英文小写及连字符，description 说明适用任务。版本如需填写，使用 metadata.version。保留原有专业方法与必要参考文件。
2. 入口只写适用范围、工作步骤和必要约束，长篇方法放入 references 并说明何时读取。不要只改文件名或 frontmatter 就宣称适配完成。

影策云端运行边界：
3. native read 只读取本轮已选技能包里的 .md、.txt、.json。当前技能路径来自系统 available_skills 的 location；同包引用用这个位置拼接包内路径。跨技能先定位目标 location，不能猜目录名或把 ../ 路径直接交给 read。目标技能未选中时说明缺少依赖。
4. 云端不提供 bash、PowerShell、Python 执行或任意磁盘读写。scripts/assets 可以保留为资料，但不能要求自动运行脚本，也不能声称它们已经执行。外部 SQLite、OCR、下载器和本地知识库未接入时必须明确说明，不能用模型记忆假冒检索结果。
5. 把本地工程文件、版本登记和检查点改为通过现有画布工具保存的文本节点；先读取真实节点和最新快照，保存成功后回读。保留原稿、已确认版本和来源，不虚构保存结果。
6. 涉及实际拆镜时，把剧情写入 script 分镜节点，把风格和用户要求作为文本节点连接到它；用 model_list 查询文本模型，再调用 storyboard 的 split/resplit。局部修改使用真实 rowIds，尊重锁定范围，不用聊天 Markdown 冒充分镜写回。
7. 涉及媒体时使用实际可用的 generate_media、canvas_create_character 等工具，字段以运行时 schema 为准。拆镜和媒体均异步提交；依赖结果时用 wait_generations，同一运行下一模型步骤收到完成通知后回读结果。submitted 只表示入队。
8. 只在用户要求的范围内创作或生成，保留报价、预算和审批要求；声明 allowed-tools 或把工具名写进 SKILL.md 不会增加云端权限。所需工具不存在时如实说明，不能编造替代执行。

交付与验证：
9. 多个技能保留为各自含 SKILL.md 的目录，整体压成一个 ZIP 合集即可；不要先把各目录分别压成 ZIP 再套一层。合集最多 64 个技能、512 个文件，压缩与解压后均不超过 20MB。单技能也可独立打包。检查 YAML、入口引用、相对路径、缺失依赖与原有方法是否保留。
10. 在安装包之外给出转换说明：保留了什么、替换了什么、哪些外部能力尚不可用、需要在影策实际验收哪些步骤。静态结构检查不能写成真实云端运行通过。`;

const packageExample = `my-skill.zip
├── SKILL.md
├── references/
│   └── workflow.md
└── assets/              # 可选，保留所需资料

# SKILL.md
---
name: my-skill
description: 说明这个技能适合处理什么任务。
metadata:
  version: "1.0.0"
---

# 技能名称
说明输入、步骤和交付结果。
需要详细方法时读取 references/workflow.md。`;

export function SkillNativeGuideModal({ open, onClose }: { open: boolean; onClose: () => void }) {
    const { message } = App.useApp();

    const copyPrompt = async () => {
        try {
            if (await copy(conversionPrompt)) {
                message.success("转换提示词已复制，可粘贴到 Codex 等编程助手");
            } else {
                message.error("复制未成功，请展开提示词后手动选择复制");
            }
        } catch {
            message.error("复制未成功，请展开提示词后手动选择复制");
        }
    };

    return (
        <AppModal open={open} title="Native 技能转换指南" width={840} onCancel={onClose} footer={<Button onClick={onClose}>关闭指南</Button>}>
            <article className="skill-native-guide" aria-label="将其他平台的技能转换为影策 native skill" data-canvas-no-zoom data-canvas-wheel-scroll>
                <p className="skill-native-guide-intro">把已有技能交给 Codex、Claude Code 或其他能编辑本地文件的编程助手，先转换、再导入影策。转换需要同时处理包结构和运行依赖；有了 SKILL.md，仍要检查其中的步骤能否在影策执行。</p>

                <section aria-labelledby="native-guide-convert">
                    <h3 id="native-guide-convert">1. 准备原包并交给编程助手</h3>
                    <ol>
                        <li>将原 ZIP 解压到单独的工作文件夹，保留原包；让助手读取整个技能目录，包括参考资料和脚本。</li>
                        <li>
                            复制下面的提示词，附上原包位置，要求输出到新目录。Codex 中也可请内置的 <code>$skill-creator</code> 协助整理。
                        </li>
                        <li>检查助手给出的转换说明，确认哪些步骤已改用画布工具、哪些外部能力仍需另行接入。</li>
                    </ol>
                    <div className="skill-native-guide-prompt">
                        <Button icon={<Copy className="size-4" aria-hidden="true" />} onClick={() => void copyPrompt()}>
                            复制转换提示词
                        </Button>
                        <details>
                            <summary>查看完整转换提示词</summary>
                            <pre tabIndex={0} aria-label="可手动复制的转换提示词">
                                <code>{conversionPrompt}</code>
                            </pre>
                        </details>
                    </div>
                </section>

                <section aria-labelledby="native-guide-runtime">
                    <h3 id="native-guide-runtime">2. 检查影策里能执行的步骤</h3>
                    <dl className="skill-native-guide-boundaries">
                        <div>
                            <dt>读参考资料</dt>
                            <dd>可以按需读取本轮已选技能里的 Markdown、TXT 和 JSON。技能之间的引用要按本轮目录定位，不能照搬电脑路径。</dd>
                        </div>
                        <div>
                            <dt>保存项目成果</dt>
                            <dd>把原来的工程文件、阶段记录和版本登记改为真实画布文档节点，写入后读取核对。</dd>
                        </div>
                        <div>
                            <dt>拆镜与媒体生成</dt>
                            <dd>使用现有分镜、媒体和角色工具。任务提交后等待完成通知，再读取结果继续同一运行。</dd>
                        </div>
                        <div>
                            <dt>脚本与外部知识库</dt>
                            <dd>技能包不会自动获得终端、磁盘或数据库权限。仅上传 Python、PowerShell 脚本或知识库路径，并不代表这些能力已接入。</dd>
                        </div>
                    </dl>
                    <details>
                        <summary>查看单个技能包的结构与入口示例</summary>
                        <pre tabIndex={0} aria-label="Native 技能包结构和 YAML 示例">
                            <code>{packageExample}</code>
                        </pre>
                    </details>
                </section>

                <section aria-labelledby="native-guide-install">
                    <h3 id="native-guide-install">3. 导入并验证</h3>
                    <ol>
                        <li>
                            在“安装技能”中选择“ZIP 技能包”，可以直接上传含多个技能目录的 ZIP 合集，预览识别到的名称和数量后统一安装。每个技能目录各有 <code>SKILL.md</code>；合集最多 64 个技能、512 个文件，整体不超过 20MB。单文件技能也可通过 Markdown
                            导入。
                        </li>
                        <li>确认名称、简介和公开状态。安装后在 Agent 技能选择中加入所需技能及依赖；旧对话更新技能后需重新选择，再启动新一轮。</li>
                        <li>先用小任务验证：是否读到参考资料、是否保存到真实画布节点、异步结果回来后是否继续。静态检查或安装成功都不能代替实际运行验收。</li>
                    </ol>
                </section>

                <p className="skill-native-guide-source">
                    标准结构参考{" "}
                    <a href="https://learn.chatgpt.com/docs/build-skills" target="_blank" rel="noreferrer">
                        OpenAI 官方技能文档
                    </a>
                    。上面的画布、工具与读取限制以影策当前运行环境为准。
                </p>
            </article>
        </AppModal>
    );
}
