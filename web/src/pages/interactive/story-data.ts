export type StoryNodeKind = "branch" | "story";

export type StoryNodeStatus = "已完成" | "写作中" | "待续写";

export interface StoryOption {
    id: string;
    label: string;
    to: string;
}

export interface StoryNode {
    id: string;
    kind: StoryNodeKind;
    title: string;
    status: StoryNodeStatus;
    body: string;
    options: StoryOption[];
    isEnding?: boolean;
    x: number;
    y: number;
}

export interface StoryEdge {
    from: string;
    to: string;
    optionId: string;
    label: string;
}

/**
 * 连线由节点选项派生，不单独存储。
 * 这样删除节点 / 改选项目标后，图上不会留下悬空连线。
 */
export function deriveEdges(nodes: StoryNode[]): StoryEdge[] {
    const ids = new Set(nodes.map((node) => node.id));
    const edges: StoryEdge[] = [];
    for (const node of nodes) {
        for (const option of node.options) {
            if (!ids.has(option.to)) continue;
            edges.push({ from: node.id, to: option.to, optionId: option.id, label: option.label });
        }
    }
    return edges;
}

/**
 * 示例剧情：同一个起点，两个结局。
 * 坐标是画布世界坐标，卡片宽 288，纵向间距约 220。
 */
export const demoStoryNodes: StoryNode[] = [
    {
        id: "E1",
        kind: "story",
        title: "末班车",
        status: "已完成",
        body: "## 第一幕\n雨夜，最后一班车停在站台。她攥着一张没有写目的地的车票，车门在面前缓缓打开。",
        options: [
            { id: "E1-a", label: "留在站台，学做公主", to: "E2" },
            { id: "E1-b", label: "跟着陌生人上车", to: "E3" },
        ],
        x: 0,
        y: 260,
    },
    {
        id: "E2",
        kind: "branch",
        title: "留下学做公主",
        status: "已完成",
        body: "## 第二幕\n她退回候车厅，玻璃上照出一个穿礼服的身影。车站深处，一扇从不对外开放的门亮起了灯。",
        options: [
            { id: "E2-a", label: "推开那扇门", to: "E4" },
            { id: "E2-b", label: "留在原地等天亮", to: "E6" },
        ],
        x: 400,
        y: 80,
    },
    {
        id: "E3",
        kind: "branch",
        title: "跟着上车",
        status: "已完成",
        body: "## 第二幕\n车厢里没有乘客，只有一排排空座位和一张摊开的剧本。列车报站声念出的，是她的名字。",
        options: [
            { id: "E3-a", label: "翻开剧本", to: "E5" },
            { id: "E3-b", label: "拉下紧急制动", to: "E7" },
        ],
        x: 400,
        y: 440,
    },
    {
        id: "E4",
        kind: "story",
        title: "礼服与王冠",
        status: "已完成",
        body: "## 第三幕\n门后是一间排练厅。镜子里的人替她戴上王冠，说：「从今天起，你只需要演好一个公主。」",
        options: [],
        isEnding: true,
        x: 800,
        y: 0,
    },
    {
        id: "E5",
        kind: "story",
        title: "剧本的最后一页",
        status: "写作中",
        body: "## 第三幕\n最后一页只写着一句话：『当列车再次停靠，请把票交给下一个上车的人。』她抬头，车窗外站着另一个自己。",
        options: [],
        isEnding: true,
        x: 800,
        y: 220,
    },
    {
        id: "E6",
        kind: "story",
        title: "天亮以后",
        status: "已完成",
        body: "## 第三幕\n第一班车进站时，候车厅已经空了。只有长椅上留下那顶不属于任何人的王冠。",
        options: [],
        isEnding: true,
        x: 800,
        y: 440,
    },
    {
        id: "E7",
        kind: "story",
        title: "停在隧道里的车",
        status: "待续写",
        body: "## 第三幕\n列车停在隧道中央，灯一盏一盏熄灭。黑暗中，有人轻轻敲了敲她身后的车窗。",
        options: [],
        isEnding: true,
        x: 800,
        y: 640,
    },
];
