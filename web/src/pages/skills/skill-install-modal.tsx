import { App, Button, Form, Input, Modal, Upload, type UploadFile } from "antd";
import { Select } from "@/components/ui/base/select";
import { Switch } from "@/components/ui/base/switch";
import { SegmentedControl } from "@/components/ui/base/segmented-control";
import { FileArchive, FileText, GitBranch, UploadCloud } from "lucide-react";
import { useEffect, useState } from "react";

import { fallbackSkillCategories } from "@/pages/skills/skill-catalog";
import { installGitHubSkill, installSkillUpload, previewSkillUpload, type Skill, type SkillUploadPreview } from "@/services/api/skills";

type InstallMode = "markdown" | "zip" | "github";

type InstallFormValues = {
    name?: string;
    description?: string;
    tag: string;
    is_public: boolean;
    url?: string;
    ref?: string;
    subdir?: string;
    autoUpdate: boolean;
};

const modeOptions = [
    {
        value: "markdown",
        label: (
            <span className="inline-flex items-center gap-2">
                <FileText className="size-3.5" />
                Markdown
            </span>
        ),
    },
    {
        value: "zip",
        label: (
            <span className="inline-flex items-center gap-2">
                <FileArchive className="size-3.5" />
                ZIP 技能包
            </span>
        ),
    },
    {
        value: "github",
        label: (
            <span className="inline-flex items-center gap-2">
                <GitBranch className="size-3.5" />
                GitHub
            </span>
        ),
    },
];

export function SkillInstallModal({ open, onClose, onInstalled, onManualCreate, onOpenGuide }: { open: boolean; onClose: () => void; onInstalled: (skills: Skill[]) => void; onManualCreate: () => void; onOpenGuide: () => void }) {
    const { message } = App.useApp();
    const [form] = Form.useForm<InstallFormValues>();
    const [mode, setMode] = useState<InstallMode>("markdown");
    const [fileList, setFileList] = useState<UploadFile[]>([]);
    const [installing, setInstalling] = useState(false);
    const [preview, setPreview] = useState<{ file: File; data: SkillUploadPreview } | null>(null);
    const [previewing, setPreviewing] = useState(false);
    const [previewError, setPreviewError] = useState("");
    const [previewRevision, setPreviewRevision] = useState(0);
    const uploadFile = fileList[0]?.originFileObj;
    const activePreview = preview?.file === uploadFile ? preview?.data : undefined;
    const collection = (activePreview?.skills.length || 0) > 1;

    useEffect(() => {
        setPreview(null);
        setPreviewError("");
        if (!open || mode !== "zip" || !uploadFile) {
            setPreviewing(false);
            return;
        }
        const controller = new AbortController();
        setPreviewing(true);
        void previewSkillUpload(uploadFile, "zip", controller.signal)
            .then((data) => {
                if (controller.signal.aborted) return;
                setPreview({ file: uploadFile, data });
                if (data.skills.length > 1) form.setFieldsValue({ name: "", description: "" });
            })
            .catch((error) => {
                if (!controller.signal.aborted) setPreviewError(error instanceof Error ? error.message : "技能包识别失败");
            })
            .finally(() => {
                if (!controller.signal.aborted) setPreviewing(false);
            });
        return () => controller.abort();
    }, [form, mode, open, previewRevision, uploadFile]);

    useEffect(() => {
        if (!open) return;
        setMode("markdown");
        setFileList([]);
        form.setFieldsValue({ tag: "creative", is_public: false, autoUpdate: true, name: "", description: "", url: "", ref: "", subdir: "" });
    }, [form, open]);

    const install = async () => {
        const values = await form.validateFields().catch(() => null);
        if (!values) return;
        const file = fileList[0]?.originFileObj;
        if (mode !== "github" && !file) {
            message.warning(`请选择一个 ${mode === "zip" ? "ZIP 技能包" : "Markdown 文件"}`);
            return;
        }
        if (mode === "zip" && (!activePreview || previewing || previewError)) {
            message.warning("请先完成技能包识别");
            return;
        }
        setInstalling(true);
        try {
            let installed: Skill[];
            if (mode === "github") {
                const result = await installGitHubSkill({
                    url: values.url || "",
                    ref: values.ref || undefined,
                    subdir: values.subdir || undefined,
                    tag: values.tag,
                    isPrivate: !values.is_public,
                    autoUpdate: values.autoUpdate,
                });
                installed = [result.skill];
            } else {
                const result = await installSkillUpload({
                    file: file as File,
                    sourceType: mode,
                    name: collection ? undefined : values.name || undefined,
                    description: collection ? undefined : values.description || undefined,
                    tag: values.tag,
                    isPrivate: !values.is_public,
                });
                installed = result.skills;
            }
            if (!installed.length) throw new Error("安装回执未包含技能，请刷新技能库核对");
            message.success(installed.length > 1 ? `已安装 ${installed.length} 个技能` : "技能已安装");
            onInstalled(installed);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "技能安装失败");
        } finally {
            setInstalling(false);
        }
    };

    return (
        <Modal
            className="skill-install-modal"
            open={open}
            width={680}
            destroyOnHidden
            closable={!installing}
            keyboard={!installing}
            mask={{ closable: !installing }}
            title="安装技能"
            onCancel={onClose}
            footer={
                <div className="flex items-center justify-between gap-3">
                    <Button type="text" disabled={installing} onClick={onManualCreate}>
                        从空白创建单文件技能
                    </Button>
                    <div className="flex gap-2">
                        <Button disabled={installing} onClick={onClose}>
                            取消
                        </Button>
                        <Button type="primary" loading={installing} disabled={mode === "zip" && (!activePreview || previewing || Boolean(previewError))} onClick={() => void install()}>
                            {collection ? `安装 ${activePreview?.skills.length} 个技能` : "安装技能"}
                        </Button>
                    </div>
                </div>
            }
        >
            <p className="mb-2 text-sm leading-6 text-foreground/55">
                支持标准 <code>SKILL.md</code>、ZIP 单技能或技能合集，以及公开 GitHub 仓库。合集只需上传一次，会自动识别每个技能的名称与简介。
            </p>
            <div className="mb-4 flex flex-wrap items-center gap-x-2 text-sm text-foreground/65">
                <span>已有其他平台的技能？</span>
                <Button type="link" onClick={onOpenGuide}>
                    查看 Native 技能转换指南
                </Button>
            </div>
            <SegmentedControl
                className="skill-install-mode"
                block
                disabled={installing}
                options={modeOptions}
                value={mode}
                onChange={(value) => {
                    setMode(value as InstallMode);
                    setFileList([]);
                }}
            />

            <Form form={form} layout="vertical" requiredMark="optional" className="skill-install-form">
                {mode === "github" ? (
                    <>
                        <Form.Item
                            name="url"
                            label="GitHub 地址"
                            rules={[
                                { required: true, message: "请填写 GitHub 仓库地址" },
                                { type: "url", message: "请输入有效链接" },
                            ]}
                        >
                            <Input type="url" inputMode="url" spellCheck={false} prefix={<GitBranch className="size-4 text-foreground/35" />} placeholder="https://github.com/owner/repository" />
                        </Form.Item>
                        <div className="grid gap-x-3 sm:grid-cols-2">
                            <Form.Item name="ref" label="分支或标签" extra="留空时使用默认分支">
                                <Input spellCheck={false} placeholder="main" />
                            </Form.Item>
                            <Form.Item name="subdir" label="技能子目录" extra="仓库仅含一个技能时可留空">
                                <Input spellCheck={false} placeholder="skills/ai-director" />
                            </Form.Item>
                        </div>
                    </>
                ) : (
                    <>
                        <Upload.Dragger
                            disabled={installing}
                            accept={mode === "zip" ? ".zip,application/zip" : ".md,.markdown,text/markdown"}
                            maxCount={1}
                            fileList={fileList}
                            beforeUpload={(file) => {
                                if (file.size > 20 * 1024 * 1024) {
                                    message.error("技能文件不能超过 20MB");
                                    return Upload.LIST_IGNORE;
                                }
                                return false;
                            }}
                            onChange={({ fileList: next }) => setFileList(next.slice(-1))}
                            onRemove={() => {
                                setFileList([]);
                                return true;
                            }}
                        >
                            <UploadCloud className="mx-auto mb-3 size-8 text-foreground/38" />
                            <div className="text-sm font-medium">拖入或选择 {mode === "zip" ? "ZIP 技能包" : "Markdown 文件"}</div>
                            <div className="mt-1 text-xs text-foreground/45">{mode === "zip" ? "支持多个技能目录各自包含 SKILL.md，上传后预览并统一安装" : "普通 .md 会作为技能入口 SKILL.md 安装"}</div>
                        </Upload.Dragger>
                        {mode === "zip" && uploadFile ? (
                            <section className="mt-4 text-sm" aria-label="技能包识别结果" aria-busy={previewing}>
                                {previewing ? <p role="status">正在识别技能包…</p> : null}
                                {previewError ? (
                                    <div role="alert">
                                        <p>{previewError}</p>
                                        <Button type="link" onClick={() => setPreviewRevision((value) => value + 1)}>
                                            重新识别
                                        </Button>
                                    </div>
                                ) : null}
                                {activePreview ? (
                                    <>
                                        <p className="mb-2">
                                            已识别 {activePreview.skills.length} 个技能，{collection ? "将保留各自的名称、资料和版本，一次安装到我的技能。" : "可在下方覆盖名称与简介。"}
                                        </p>
                                        <ul className="max-h-52 space-y-2 overflow-y-auto" data-canvas-wheel-scroll>
                                            {activePreview.skills.map((skill) => (
                                                <li key={skill.entryPath} className="border-b border-border pb-2">
                                                    <p className="font-medium">
                                                        {skill.name}
                                                        <span className="ml-2 font-normal text-foreground/55">
                                                            {skill.fileCount} 个文件{skill.version ? ` · ${skill.version}` : ""}
                                                        </span>
                                                    </p>
                                                    <p className="break-words text-xs text-foreground/65">{skill.description}</p>
                                                </li>
                                            ))}
                                        </ul>
                                        {activePreview.ignoredFileCount > 0 ? <p className="mt-2 text-xs text-foreground/55">另有 {activePreview.ignoredFileCount} 个技能目录外的文件不加入技能库。</p> : null}
                                    </>
                                ) : null}
                            </section>
                        ) : null}
                        {!collection ? (
                            <div className="mt-4 grid gap-x-3 sm:grid-cols-2">
                                <Form.Item name="name" label="覆盖名称" extra="可选，留空时自动读取">
                                    <Input maxLength={80} autoComplete="off" />
                                </Form.Item>
                                <Form.Item name="description" label="覆盖简介" extra="可选，留空时自动读取">
                                    <Input maxLength={500} autoComplete="off" />
                                </Form.Item>
                            </div>
                        ) : null}
                    </>
                )}

                <div className="grid gap-x-3 sm:grid-cols-2">
                    <Form.Item name="tag" label="技能分类" rules={[{ required: true, message: "请选择技能分类" }]}>
                        <Select options={fallbackSkillCategories} />
                    </Form.Item>
                    <Form.Item name="is_public" label="公开状态" valuePropName="checked" extra="公开后其他用户可以加入使用。">
                        <Switch checkedChildren="公开" unCheckedChildren="私有" />
                    </Form.Item>
                </div>
                {mode === "github" ? (
                    <Form.Item name="autoUpdate" label="自动同步" valuePropName="checked" extra="后台每 6 小时检查一次提交版本，并记录最近检查与同步时间。">
                        <Switch checkedChildren="开启" unCheckedChildren="关闭" />
                    </Form.Item>
                ) : null}
            </Form>
        </Modal>
    );
}
