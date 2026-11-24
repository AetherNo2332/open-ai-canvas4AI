import { ArrowUpRight, Clapperboard, Film, NotebookPen } from "lucide-react";
import { BrandLogo } from "@/components/brand/brand-logo";
import { agentCopy, type CanvasAppearance } from "@/lib/agent/agent-appearance";

type AgentWelcomeProps = {
    appearance: CanvasAppearance;
    onDraftPrompt: (prompt: string) => void;
    disabled?: boolean;
    title?: string;
    description?: string;
};

const startingPoints = [
    { icon: NotebookPen, title: "构思故事", subtitle: "主题、人物与冲突", prompt: "我想创作一段短片。请先询问我的想法，再一起梳理故事的主题、人物和核心冲突。" },
    { icon: Clapperboard, title: "规划一组镜头", subtitle: "景别、动作与节奏", prompt: "请先问我这场戏的内容，再给出一组分镜草案，包含景别、动作和节奏。只出文字建议，不修改画布或提交生成任务。" },
    { icon: Film, title: "探索视觉风格", subtitle: "色彩、光线与影调", prompt: "请先问我想呈现的情绪，再给出两种视觉风格方向，并比较它们的色彩、光线和影调。暂不生成素材。" },
];

export function AgentWelcome({ appearance, onDraftPrompt, disabled = false, title, description }: AgentWelcomeProps) {
    return (
        <section className="agent-welcome" aria-label="开始 Agent 创作">
            <div className="agent-welcome-intro">
                {/* 品牌 logo 由后台外观配置（logoUrl / darkLogoUrl）下发；未配置时回退内置品牌标。 */}
                <BrandLogo className="agent-welcome-logo" fallback={<img src="/logo.svg" alt="" aria-hidden="true" className="agent-welcome-logo" draggable={false} />} />
                <h2>{title || agentCopy(appearance.welcomeTitle, appearance.agentName)}</h2>
                <p>{description || agentCopy(appearance.welcomeDescription, appearance.agentName)}</p>
            </div>
            <div className="agent-welcome-actions">
                {startingPoints.map(({ icon: Icon, title, subtitle, prompt }) => (
                    <button key={title} type="button" disabled={disabled} onClick={() => onDraftPrompt(prompt)}>
                        <Icon aria-hidden="true" />
                        <span>
                            <strong>{title}</strong>
                            <small>{subtitle}</small>
                        </span>
                        <ArrowUpRight className="agent-welcome-arrow" aria-hidden="true" />
                    </button>
                ))}
            </div>
        </section>
    );
}
