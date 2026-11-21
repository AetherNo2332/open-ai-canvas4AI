import { demoStoryNodes, type StoryNode } from "./story-data";

export interface StoryProject {
    id: string;
    title: string;
    nodes: StoryNode[];
    createdAt: number;
    updatedAt: number;
}

const STORAGE_KEY = "yingce.interactive.projects.v1";

function newId() {
    return `story_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`;
}

function cloneNodes(nodes: StoryNode[]): StoryNode[] {
    return nodes.map((node) => ({ ...node, options: node.options.map((option) => ({ ...option })) }));
}

function readAll(): StoryProject[] {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) return [];
        const parsed = JSON.parse(raw);
        if (!Array.isArray(parsed)) return [];
        return parsed.filter((item): item is StoryProject => Boolean(item) && typeof item.id === "string" && typeof item.title === "string" && Array.isArray(item.nodes));
    } catch {
        return [];
    }
}

function writeAll(projects: StoryProject[]) {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(projects));
}

/** 首次打开时把示例剧情落成一个真正可编辑的项目。 */
function seedIfEmpty(): StoryProject[] {
    const projects = readAll();
    if (projects.length > 0) return projects;
    const now = Date.now();
    const demo: StoryProject = {
        id: newId(),
        title: "末班车（示例）",
        nodes: cloneNodes(demoStoryNodes),
        createdAt: now,
        updatedAt: now,
    };
    writeAll([demo]);
    return [demo];
}

export function listProjects(): StoryProject[] {
    return seedIfEmpty().sort((a, b) => b.updatedAt - a.updatedAt);
}

export function getProject(id: string): StoryProject | null {
    return seedIfEmpty().find((project) => project.id === id) ?? null;
}

export function saveProject(project: StoryProject) {
    const projects = seedIfEmpty();
    const index = projects.findIndex((item) => item.id === project.id);
    if (index >= 0) projects[index] = project;
    else projects.push(project);
    writeAll(projects);
}

export function createProject(title: string): StoryProject {
    const now = Date.now();
    const project: StoryProject = {
        id: newId(),
        title: title.trim() || "未命名互动剧",
        nodes: [
            {
                id: "E1",
                kind: "story",
                title: "开场",
                status: "写作中",
                body: "## 第一幕\n写下故事的第一个画面。",
                options: [],
                x: 0,
                y: 160,
            },
        ],
        createdAt: now,
        updatedAt: now,
    };
    const projects = seedIfEmpty();
    projects.push(project);
    writeAll(projects);
    return project;
}

export function deleteProject(id: string) {
    writeAll(seedIfEmpty().filter((project) => project.id !== id));
}

export function formatUpdatedAt(timestamp: number) {
    const date = new Date(timestamp);
    const now = new Date();
    const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
    if (sameDay) {
        return `今天 ${date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}`;
    }
    return date.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
}
