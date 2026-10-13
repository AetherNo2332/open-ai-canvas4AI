
/** Keep settings beside the viewport and their actions in the inspector's always-visible footer. */
export function inspectorToolLayout(html: string) {
    let footer = '';
    const body = html.replace(/<button\b[^>]*data-act="(?:cinema|lighting)-[^>]*>[\s\S]*?<\/button>/g, button => { footer += button; return ''; })
        .replace(/<div class="cinema-pair">\s*<\/div>/g, '');
    return { body, footer };
}
