import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ArrowLeft, GitBranch, Layers, Plus, Sparkles, Trash2 } from "lucide-react";

import { createProject, deleteProject, formatUpdatedAt, listProjects, type StoryProject } from "./storage";
import "./interactive.css";

function countOptions(project: StoryProject) {
    return project.nodes.reduce((total, node) => total + node.options.length, 0);
}

function countEndings(project: StoryProject) {
    return project.nodes.filter((node) => node.isEnding).length;
}

export default function InteractiveStoryListPage() {
    const navigate = useNavigate();
    const [projects, setProjects] = useState<StoryProject[]>(() => listProjects());

    const totalNodes = useMemo(() => projects.reduce((total, project) => total + project.nodes.length, 0), [projects]);

    const handleCreate = () => {
        const project = createProject(`未命名互动剧 ${projects.length + 1}`);
        navigate(`/interactive/${project.id}`);
    };

    const handleDelete = (project: StoryProject) => {
        if (!window.confirm(`删除「${project.title}」？删除后无法恢复。`)) return;
        deleteProject(project.id);
        setProjects(listProjects());
    };

    return (
        <div className="story-shell">
            <header className="story-topbar">
                <div className="story-topbar__left">
                    <Link to="/" className="story-icon-button" title="返回创作空间">
                        <ArrowLeft className="size-4" />
                    </Link>
                    <div className="story-topbar__title">
                        <div className="story-topbar__eyebrow">
                            <GitBranch className="size-3.5" />
                            互动剧
                        </div>
                        <div className="story-topbar__name">我的互动剧</div>
                    </div>
                    <span className="story-topbar__meta">
                        {projects.length} 个项目 · {totalNodes} 个节点
                    </span>
                </div>
                <div className="story-topbar__right">
                    <button type="button" className="story-button is-primary" onClick={handleCreate}>
                        <Plus className="size-3.5" />
                        新建互动剧
                    </button>
                </div>
            </header>

            <main className="story-home">
                <section className="story-home__hero">
                    <span className="story-home__badge">
                        <Sparkles className="size-3.5" />
                        试运行
                    </span>
                    <h1>从一个画面开始，搭一张自己的剧情总图</h1>
                    <p>新建项目 → 添加剧情 / 分支节点 → 用选项把它们连起来。所有修改自动保存在本机浏览器。</p>
                    <button type="button" className="story-button is-primary is-large" onClick={handleCreate}>
                        <Plus className="size-4" />
                        新建互动剧
                    </button>
                </section>

                {projects.length === 0 ? (
                    <div className="story-home__empty">还没有互动剧，点上面的按钮新建一个。</div>
                ) : (
                    <section className="story-projects">
                        {projects.map((project) => (
                            <article
                                key={project.id}
                                className="story-project-card"
                                role="button"
                                tabIndex={0}
                                onClick={() => navigate(`/interactive/${project.id}`)}
                                onKeyDown={(event) => {
                                    if (event.key === "Enter" || event.key === " ") {
                                        event.preventDefault();
                                        navigate(`/interactive/${project.id}`);
                                    }
                                }}
                            >
                                <div className="story-project-card__head">
                                    <span className="story-project-card__kind">
                                        <GitBranch className="size-3.5" />
                                        互动剧
                                    </span>
                                    <button
                                        type="button"
                                        className="story-icon-button is-danger"
                                        title="删除项目"
                                        onClick={(event) => {
                                            event.stopPropagation();
                                            handleDelete(project);
                                        }}
                                    >
                                        <Trash2 className="size-3.5" />
                                    </button>
                                </div>
                                <h2 className="story-project-card__title">{project.title}</h2>
                                <div className="story-project-card__meta">
                                    <span>
                                        <Layers className="size-3.5" />
                                        {project.nodes.length} 个节点
                                    </span>
                                    <span>{countOptions(project)} 条分支</span>
                                    <span>{countEndings(project)} 个结局</span>
                                </div>
                                <div className="story-project-card__foot">
                                    <span>更新于 {formatUpdatedAt(project.updatedAt)}</span>
                                    <span className="story-project-card__open">打开 →</span>
                                </div>
                            </article>
                        ))}
                    </section>
                )}
            </main>
        </div>
    );
}
