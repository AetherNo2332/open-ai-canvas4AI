/**
 * 画布临时浮层（右键菜单、下拉、拖动面板）可以使用的右边界（视口坐标）。
 * Agent 面板停靠时与编辑区并排，浮层夹在编辑区右缘内，不伸到停靠栏上；
 * 浮动或收起时返回视口宽度，浮层靠 --z-agent-overlay 压在浮动面板之上。
 */
export function canvasOverlayRightEdge(doc: Document = document) {
    const view = doc.defaultView;
    const viewportRight = view ? view.innerWidth : 0;
    const docked = doc.querySelector<HTMLElement>('.canvas-agent-panel.is-docked[data-agent-open="true"]');
    if (!docked) return viewportRight;
    const left = docked.getBoundingClientRect().left;
    return left > 0 ? Math.min(viewportRight, left) : viewportRight;
}
