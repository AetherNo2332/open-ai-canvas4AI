import { Alert, Button } from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { Switch } from "@/pages/admin/ui/controls";
import { getAdminFeatureAvailability, updateAdminFeatureAvailability } from "@/services/api/auth";
import { useUserStore } from "@/stores/use-user-store";

export default function AgentSubagentsAvailabilitySection() {
    const userId = useUserStore((state) => state.user?.id);
    const client = useQueryClient();
    const queryKey = ["admin-agent-subagents-availability", userId];
    const config = useQuery({
        queryKey,
        queryFn: async () => {
            const result = await getAdminFeatureAvailability();
            if (typeof result.features.agentSubagentsEnabled !== "boolean") throw new Error("当前后端未返回动态子代理平台开关，请检查部署版本");
            return result;
        },
        enabled: Boolean(userId),
    });
    const save = useMutation({
        mutationFn: async (enabled: boolean) => {
            const result = await updateAdminFeatureAvailability({ agentSubagentsEnabled: enabled });
            if (result.features.agentSubagentsEnabled !== enabled) throw new Error("动态子代理平台开关未保存，请重新读取配置");
            return result;
        },
        onSuccess: (result) => {
            if (useUserStore.getState().user?.id !== userId) return;
            client.setQueryData(queryKey, result);
            useUserStore.getState().setFeatures(result.features);
        },
    });
    const enabled = config.data?.features.agentSubagentsEnabled === true;
    const error = save.error || config.error;
    return (
        <>
            <div className="agent-settings-heading">
                <h2 id="agent-subagents-heading" className="text-base font-semibold">
                    动态子代理平台能力
                </h2>
                <Button
                    icon={<RefreshCw size={14} />}
                    disabled={config.isFetching || save.isPending}
                    onClick={() => {
                        save.reset();
                        void config.refetch();
                    }}
                >
                    重新读取配置
                </Button>
            </div>
            <p className="text-sm text-foreground/60">控制动态子代理平台能力，默认关闭。切换后立即保存；父 Agent 是否在下一轮召唤子代理，由各画布中的持续授权开关决定。</p>
            <div className="flex flex-wrap items-center justify-between gap-3">
                <span>启用动态子代理平台能力</span>
                <div className="flex items-center gap-3">
                    <span role="status" className="text-sm text-foreground/60">
                        {save.isPending ? "保存中…" : config.isFetching ? "读取中…" : config.data ? (enabled ? "已启用" : "已关闭") : "配置尚未读取"}
                    </span>
                    <Switch aria-label="启用动态子代理平台能力" checked={enabled} loading={save.isPending} disabled={!config.data || config.isFetching || save.isPending || Boolean(config.error)} onChange={(value) => save.mutate(value)} />
                </div>
            </div>
            {error ? <Alert type="error" showIcon title={error.message} /> : null}
        </>
    );
}
