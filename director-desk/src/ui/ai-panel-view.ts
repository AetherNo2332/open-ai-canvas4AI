import { floatingPanel } from './floating-panel.ts';
/** Presentation and navigation for the assistant; provider state stays in ai-panel. */
export function createAIPanel(desktop: boolean) {
    const toggle = document.createElement('button'); toggle.id = 'ai-toggle'; toggle.className = 'subtle'; toggle.textContent = 'AI';
    toggle.setAttribute('aria-expanded', 'false'); toggle.setAttribute('aria-controls', 'ai-panel');
    document.querySelector('.header-actions')!.prepend(toggle);
    const panel = document.createElement('aside'); panel.id = 'ai-panel'; panel.hidden = true; panel.setAttribute('aria-label', '导演助手');
    panel.innerHTML = `<div id="ai-resize" role="separator" tabindex="0" aria-orientation="horizontal" aria-label="调整助手大小" title="拖动右下角调整大小，方向键微调，双击复位"></div>
<header class="ai-header" title="拖动移动窗口"><div><strong>导演助手</strong></div><div class="ai-window-buttons"><button id="ai-collapse" class="icon-button" aria-label="收起导演助手">−</button><button id="ai-close" class="icon-button" aria-label="关闭 AI 面板">×</button></div></header>
<nav class="ai-navigation" aria-label="助手功能"><button id="ai-chat-toggle" data-ai-view="chat" aria-pressed="true">对话</button><button id="ai-settings-toggle" data-ai-view="settings" aria-pressed="false">渠道设置</button><button id="ai-skills-toggle" data-ai-view="skills" aria-pressed="false">技能</button><button id="ai-host-toggle" data-ai-view="host" aria-pressed="false">云端接入</button></nav>
<section id="ai-skills" class="ai-view" hidden></section>
<section id="ai-chat" class="ai-view ai-chat"><div class="ai-controls"><label>渠道<select id="ai-channel" aria-label="AI 渠道"></select></label><label>模式<select id="ai-mode" aria-label="AI 模式"><option value="execute">执行</option><option value="discuss">讨论</option></select></label></div><div class="ai-context-row"><button id="ai-scene-prompt" class="subtle" type="button">本场提示词</button></div><textarea id="ai-transcript" readonly aria-label="AI 对话与操作记录" placeholder="任务进展和操作结果将显示在这里。"></textarea><label class="ai-prompt-label" for="ai-prompt">当前任务</label><textarea id="ai-prompt" maxlength="20000" placeholder="例如：安排三人的走位，检查运镜和出画情况。" aria-label="AI 任务"></textarea><div class="ai-buttons ai-task-buttons"><button id="ai-send" class="primary">发送</button><button id="ai-stop" disabled>停止</button><button id="ai-new">新对话</button><button id="ai-undo">撤销一步</button></div></section>
<form id="ai-settings" class="ai-view" hidden><div class="ai-settings-fields"><label>编辑渠道<select id="ai-edit-channel"><option value="">新建</option></select></label><label>名称<input id="ai-name" maxlength="80" placeholder="我的渠道"/></label><label>接口协议<select id="ai-protocol"><option value="chat">OpenAI Chat 兼容</option><option value="responses">OpenAI Responses</option><option value="anthropic">Anthropic Messages</option></select></label><label>模型 ID<input id="ai-model" placeholder="模型名称"/></label><label class="ai-full-row">接口地址<input id="ai-url" placeholder="https://api.deepseek.com/v1"/></label><label class="ai-full-row">密钥<input id="ai-key" type="password" autocomplete="off" placeholder="留空保留同渠道密钥"/></label><label class="ai-check"><input id="ai-remember" type="checkbox"/>记住密钥</label><label class="ai-check"><input id="ai-stream" type="checkbox" checked/>流式响应</label><label>输出 tokens（0 自动）<input id="ai-max-tokens" type="number" min="0" step="1" value="0"/></label><label>任务轮数（0 不限）<input id="ai-max-rounds" type="number" min="0" step="1" value="64"/></label></div><div class="ai-buttons"><button type="submit" class="primary">保存渠道</button><button type="button" id="ai-test">连通测试</button><button type="button" id="ai-remove" class="subtle">删除</button></div></form>
<section id="ai-host" class="ai-view" hidden><div class="ai-host-heading"><p>云端 AI 由影策 Agent 接入；技能随工作台内置。</p></div><div class="ai-connection"><span>通道状态</span><div id="ai-host-status">未连接</div></div></section>
<footer class="ai-footer"><textarea id="ai-status" readonly role="status" aria-label="助手运行状态">${desktop ? '就绪' : '请在桌面版使用 AI 与 MCP'}</textarea><button id="ai-background-stop" class="subtle" hidden>停止任务</button></footer>`;
    const changes = document.createElement('button'); changes.id = 'ai-changes'; changes.className = 'subtle'; changes.textContent = '修改定位';
    panel.querySelector('.ai-context-row')!.append(changes);
    document.body.append(panel);
    const find = <T extends HTMLElement = HTMLInputElement>(id: string) => panel.querySelector<T>('#' + id)!;
    const windowLayout = floatingPanel(panel, panel.querySelector('.ai-header')!, find('ai-resize'), find<HTMLButtonElement>('ai-collapse'), 'director-ai-window-v1');
    const open = (value: boolean) => { panel.hidden = !value; if (value) windowLayout.refresh(); toggle.setAttribute('aria-expanded', String(value)); if (!value) { panel.querySelector('[data-parent-page-back]')?.remove(); toggle.focus({ preventScroll: true }); } };
    toggle.onclick = () => open(panel.hidden); find('ai-close').onclick = () => open(false);
    const navigate = (view: string) => {
        for (const name of ['chat', 'settings', 'skills', 'host']) find(name === 'settings' ? 'ai-settings' : 'ai-' + name).hidden = name !== view;
        panel.querySelectorAll<HTMLElement>('[data-ai-view]').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.aiView === view)));
        panel.dataset.aiPage = view;
    };
    panel.querySelectorAll<HTMLElement>('[data-ai-view]').forEach(button => button.onclick = () => navigate(button.dataset.aiView!));
    panel.addEventListener('keydown', event => { if (event.key === 'Escape' && (event.target as HTMLElement).tagName !== 'SELECT') { event.preventDefault(); event.stopPropagation(); open(false); } });
    navigate('chat');
    return { panel, find, navigate, open };
}
