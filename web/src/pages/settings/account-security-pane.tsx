import { App, Button, Form, Input } from "antd";
import { KeyRound, ShieldCheck } from "lucide-react";
import { useState } from "react";

import { SettingsRow } from "@/components/ui/product/settings-row";
import { changePassword } from "@/services/api/auth";
import { useUserStore } from "@/stores/use-user-store";

type PasswordFormValues = {
    currentPassword: string;
    newPassword: string;
    confirmPassword: string;
};

/**
 * 账户安全面板：登录状态下自助改密。
 * 服务端会校验当前密码，成功后保留当前会话并让其他设备登录失效。
 */
export function AccountSecurityPane() {
    const { message } = App.useApp();
    const [form] = Form.useForm<PasswordFormValues>();
    const [saving, setSaving] = useState(false);
    const user = useUserStore((state) => state.user);

    const submit = async () => {
        let values: PasswordFormValues;
        try {
            values = await form.validateFields();
        } catch {
            return;
        }
        setSaving(true);
        try {
            await changePassword({ currentPassword: values.currentPassword, newPassword: values.newPassword });
            form.resetFields();
            message.success("密码已更新，其他设备需要重新登录");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "修改密码失败");
        } finally {
            setSaving(false);
        }
    };

    return (
        <>
            <div className="settings-pane-header">
                <div className="min-w-0">
                    <h2>账户安全</h2>
                    <p>修改登录密码。修改后当前设备保持登录，其他设备的登录状态会失效。</p>
                </div>
            </div>
            <div className="settings-section">
                <SettingsRow label="用户名" description="登录账号，暂不支持修改" control={<span className="text-sm text-foreground/70">{user?.username || "—"}</span>} controlClassName="max-w-[240px] truncate text-right" />
                <SettingsRow
                    label="邮箱"
                    description={user?.email ? "可用于找回密码" : "未绑定邮箱，忘记密码时需要联系管理员"}
                    control={<span className="text-sm text-foreground/70">{user?.email || "未绑定"}</span>}
                    controlClassName="max-w-[240px] truncate text-right"
                />
            </div>
            <div className="settings-section mt-4">
                <div className="settings-preference-heading">
                    <h3 className="flex items-center gap-2">
                        <KeyRound className="size-4" aria-hidden />
                        修改密码
                    </h3>
                    <p>新密码至少 8 位；修改后请用新密码登录其他设备。</p>
                </div>
                <Form form={form} layout="vertical" requiredMark={false} className="max-w-[420px]" onFinish={() => void submit()}>
                    <Form.Item name="currentPassword" label="当前密码" rules={[{ required: true, message: "请输入当前密码" }]}>
                        <Input.Password autoComplete="current-password" placeholder="输入当前登录密码" />
                    </Form.Item>
                    <Form.Item
                        name="newPassword"
                        label="新密码"
                        rules={[
                            { required: true, message: "请输入新密码" },
                            { min: 8, message: "密码至少 8 位" },
                        ]}
                    >
                        <Input.Password autoComplete="new-password" placeholder="至少 8 位" />
                    </Form.Item>
                    <Form.Item
                        name="confirmPassword"
                        label="确认新密码"
                        dependencies={["newPassword"]}
                        rules={[
                            { required: true, message: "请再次输入新密码" },
                            ({ getFieldValue }) => ({
                                validator(_rule, value) {
                                    if (!value || getFieldValue("newPassword") === value) return Promise.resolve();
                                    return Promise.reject(new Error("两次输入的新密码不一致"));
                                },
                            }),
                        ]}
                    >
                        <Input.Password autoComplete="new-password" placeholder="再次输入新密码" />
                    </Form.Item>
                    <div className="flex flex-wrap items-center gap-3">
                        <Button type="primary" htmlType="submit" loading={saving}>
                            更新密码
                        </Button>
                        <span className="flex items-center gap-1.5 text-xs text-foreground/55">
                            <ShieldCheck className="size-3.5" aria-hidden />
                            忘记当前密码？退出登录后在登录页用邮箱验证码重置。
                        </span>
                    </div>
                </Form>
            </div>
        </>
    );
}
