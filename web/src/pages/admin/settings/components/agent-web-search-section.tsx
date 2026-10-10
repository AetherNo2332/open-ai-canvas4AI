import { Alert, App, Button, Input, Skeleton } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { RefreshCw, Save } from "lucide-react";
import { Switch } from "@/pages/admin/ui/controls";
import { getAgentWebSearchSetting, updateAgentWebSearchSetting } from "@/services/api/admin-agent-settings";
import { useUserStore } from "@/stores/use-user-store";

export default function AgentWebSearchSection() {
    const { message } = App.useApp();
    const userId = useUserStore((state) => state.user?.id);
    const client = useQueryClient();
    const queryKey = ["admin-agent-web-search", userId];
    const config = useQuery({ queryKey, queryFn: getAgentWebSearchSetting, enabled: Boolean(userId) });
    const [enabled, setEnabled] = useState(false);
    const [apiKey, setApiKey] = useState("");
    const [clearApiKey, setClearApiKey] = useState(false);
    useEffect(() => {
        setEnabled(config.data?.setting.enabled || false);
        setApiKey("");
        setClearApiKey(false);
    }, [userId, config.data]);
    const save = useMutation({
        mutationFn: updateAgentWebSearchSetting,
        onSuccess: (result) => {
            if (useUserStore.getState().user?.id !== userId) return;
            client.setQueryData(queryKey, result);
            setApiKey("");
            setClearApiKey(false);
            void message.success("联网搜索配置已保存");
        },
    });
    const setting = config.data?.setting;
    const hasKey = Boolean(apiKey.trim()) || (setting?.hasApiKey && !clearApiKey);
    const dirty = Boolean(setting && (enabled !== setting.enabled || apiKey.trim() || clearApiKey));
    const busy = save.isPending || config.isFetching;
    const error = save.error || config.error;
    return (
        <>
            <div className="agent-settings-heading">
                <h2 id="agent-web-search-heading" className="text-base font-semibold">
                    联网搜索 · Tavily
                </h2>
                <Button
                    icon={<RefreshCw size={14} />}
                    disabled={busy}
                    onClick={() => {
                        save.reset();
                        void config.refetch();
                    }}
                >
                    重新读取配置
                </Button>
            </div>
            <p className="text-sm text-foreground/60">开启后，父 Agent 和子 Agent 可以搜索网页并引用来源。每次使用基础搜索，最多返回 5 条结果；关闭后阻止新的搜索调用。</p>
            {setting ? (
                <div className="space-y-4">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                        <span>启用联网搜索</span>
                        <Switch aria-label="启用联网搜索" checked={enabled} disabled={busy} onChange={setEnabled} />
                    </div>
                    <label className="agent-settings-field block space-y-2">
                        <span className="block text-sm font-medium">Tavily API Key</span>
                        <Input.Password
                            aria-label="Tavily API Key"
                            autoComplete="new-password"
                            value={apiKey}
                            disabled={busy}
                            placeholder={setting.hasApiKey && !clearApiKey ? "已配置；留空保留，输入新密钥替换" : "输入 Tavily API Key"}
                            onChange={(event) => {
                                setApiKey(event.target.value);
                                setClearApiKey(false);
                            }}
                        />
                        <span role="status" className="block text-xs text-foreground/60">
                            {clearApiKey ? "保存后清空密钥" : setting.hasApiKey ? "密钥已加密保存" : "尚未配置密钥"}
                        </span>
                    </label>
                    <div className="flex flex-wrap items-center gap-3">
                        <Button
                            type="primary"
                            icon={<Save size={14} />}
                            loading={save.isPending}
                            disabled={busy || !dirty || (enabled && !hasKey)}
                            onClick={() => save.mutate({ enabled, apiKey: apiKey.trim(), clearApiKey, expectedRevision: setting.revision })}
                        >
                            保存联网搜索配置
                        </Button>
                        <Button
                            disabled={busy || !setting.hasApiKey || clearApiKey}
                            onClick={() => {
                                setClearApiKey(true);
                                setApiKey("");
                                setEnabled(false);
                            }}
                        >
                            清空密钥
                        </Button>
                    </div>
                    {enabled && !hasKey ? <Alert type="warning" title="请配置 Tavily API Key 后再启用联网搜索。" /> : null}
                </div>
            ) : config.isPending ? (
                <Skeleton active />
            ) : null}
            {error ? <Alert type="error" showIcon title={error.message} /> : null}
        </>
    );
}
