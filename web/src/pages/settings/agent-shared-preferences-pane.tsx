import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Form, Input, Select, Skeleton } from "antd";

import { getAgentSharedPreferences, saveAgentSharedPreferences, type AgentSharedPreferences } from "@/services/api/agent-shared-preferences";
import { ApiError } from "@/services/api/request";
import { useUserStore } from "@/stores/use-user-store";

const toneOptions = [
    { value: "neutral", label: "中性" },
    { value: "professional", label: "专业" },
    { value: "casual", label: "轻松" },
];
const verbosityOptions = [
    { value: "concise", label: "简洁" },
    { value: "balanced", label: "适中" },
    { value: "detailed", label: "详细" },
];
const formatOptions = [
    { value: "auto", label: "自动" },
    { value: "prose", label: "段落" },
    { value: "bullets", label: "要点列表" },
];

function PreferenceForm({ preferences, onSave, onReload }: { preferences: AgentSharedPreferences; onSave: (draft: AgentSharedPreferences) => Promise<AgentSharedPreferences>; onReload: () => void }) {
    const save = useMutation({ mutationFn: onSave });
    const conflict = save.error instanceof ApiError && save.error.reason === "agent_preferences_conflict";
    return (
        <Form layout="vertical" initialValues={preferences} onFinish={(values: Omit<AgentSharedPreferences, "revision">) => save.mutate({ ...values, revision: preferences.revision })}>
            <Form.Item name="language" label="语言">
                <Input maxLength={12} placeholder="跟随本轮输入，例如 zh-CN" />
            </Form.Item>
            <Form.Item name="tone" label="语气">
                <Select allowClear placeholder="不设置" options={toneOptions} />
            </Form.Item>
            <Form.Item name="verbosity" label="详略">
                <Select allowClear placeholder="不设置" options={verbosityOptions} />
            </Form.Item>
            <Form.Item name="format" label="输出格式">
                <Select allowClear placeholder="不设置" options={formatOptions} />
            </Form.Item>
            {save.error && <Alert type="error" showIcon message={save.error.message} action={conflict ? <Button onClick={onReload}>重新加载</Button> : undefined} />}
            <Button htmlType="submit" type="primary" loading={save.isPending} disabled={conflict}>
                保存共享偏好
            </Button>
        </Form>
    );
}

export function AgentSharedPreferencesPane() {
    const userID = useUserStore((state) => state.user?.id);
    const queryClient = useQueryClient();
    const { message } = App.useApp();
    const queryKey = ["agent-shared-preferences", userID] as const;
    const query = useQuery({
        queryKey,
        queryFn: ({ signal }) => getAgentSharedPreferences(signal),
        enabled: Boolean(userID),
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
    });
    if (!userID) return null;
    if (query.isPending) return <Skeleton active />;
    if (!query.data) return <Alert type="error" message={query.error.message} action={<Button onClick={() => void query.refetch()}>重试</Button>} />;
    const save = async (draft: AgentSharedPreferences) => {
        const saved = await saveAgentSharedPreferences(draft);
        queryClient.setQueryData(queryKey, saved);
        message.success("共享输出偏好已保存");
        return saved;
    };
    return (
        <section className="settings-section">
            <h3>Agent 共享输出偏好</h3>
            <p>画布和短剧的新运行共用这里设置的偏好。原有运行保留开始时的偏好。</p>
            <p>新运行只读取这里设置的输出偏好。旧版自由文本提示词和账户记忆仍可查看。</p>
            <p>业务记忆默认关闭，启用后仅同一业务对象的会话共享记忆。</p>
            <PreferenceForm key={`${userID}:${query.data.revision}`} preferences={query.data} onSave={save} onReload={() => void query.refetch()} />
        </section>
    );
}
