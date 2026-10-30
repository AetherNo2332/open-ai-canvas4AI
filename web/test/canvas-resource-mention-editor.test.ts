import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { moduleGroupSource } from "./helpers/module-group-source";

function source(path: string) {
    return readFileSync(resolve(import.meta.dir, path), "utf8");
}

describe("canvas resource mention editor", () => {
    test("refreshes async reference previews even when prompt text has not changed", () => {
        const component = moduleGroupSource("components/canvas/canvas-resource-mention-textarea.tsx");
        const unchangedTextBranch = component.match(/if \(currentValue === value && lastRenderedValueRef.current === value\) \{([^}]+)\}/)?.[1] || "";
        expect(unchangedTextBranch).toContain("syncInlineMentionPreviews(editor, activeReferences)");
        // syncInlineMentionPreviews 随拆分移到 canvas-mention-chips.tsx；只截取该函数本体做断言。
        const chips = source("../src/components/canvas/canvas-mention-chips.tsx");
        const syncStart = chips.indexOf("function syncInlineMentionPreviews(");
        const sync = chips.slice(syncStart, chips.indexOf("\n}\n", syncStart) + 2);
        expect(sync).toContain('byId.get(chip.dataset.mentionReferenceId || "")');
        expect(sync).toContain('preview.getAttribute("src") !== src');
        expect(sync).toContain("preview.replaceWith(createInlinePreview(reference))");
        expect(sync).not.toContain("replaceChildren");
        expect(sync).not.toContain("onChange(");
    });

    test("uses stable component classes for inline media references", () => {
        const component = moduleGroupSource("components/canvas/canvas-resource-mention-textarea.tsx");

        expect(component).toContain("chip.className = `canvas-resource-inline-mention");
        expect(component).toContain("canvas-resource-inline-preview is-${reference.kind}");
        expect(component).not.toContain("size-[1.18em]");
    });

    test("clamps native media dimensions so video previews cannot cover prompt text", () => {
        const css = source("../src/styles/globals.css");
        const previewRule = css.match(/\.canvas-resource-inline-preview \{[^}]+}/)?.[0] || "";

        expect(previewRule).toContain("width: var(--canvas-mention-chip-preview-size)");
        expect(previewRule).toContain("min-width: var(--canvas-mention-chip-preview-size)");
        expect(previewRule).toContain("max-width: var(--canvas-mention-chip-preview-size)");
        expect(previewRule).toContain("height: var(--canvas-mention-chip-preview-size)");
        expect(previewRule).toContain("flex: 0 0 var(--canvas-mention-chip-preview-size)");
        expect(previewRule).toContain("object-fit: cover");
    });

    test("exposes inline image references as replacement drop targets", () => {
        const component = moduleGroupSource("components/canvas/canvas-resource-mention-textarea.tsx");
        const css = source("../src/styles/globals.css");

        expect(component).toContain("activeDropReferenceId?: string | null");
        expect(component).toContain("onReferenceFilesDrop?:");
        expect(component).toContain("chip.dataset.mentionReferenceId = reference.id");
        expect(component).toContain('chip.classList.toggle("is-replace-target"');
        expect(component).toContain("onReferenceFilesDrop(reference, files)");
        expect(css).toContain(".canvas-resource-inline-mention.is-replace-target");
        expect(css).toContain('content: "替换"');
    });

    test("resolves storage-backed previews and renders a visible loading spinner", () => {
        const editor = moduleGroupSource("components/canvas/canvas-resource-mention-textarea.tsx");
        const panel = moduleGroupSource("components/canvas/canvas-node-prompt-panel.tsx");
        const configComposer = source("../src/components/canvas/canvas-config-composer.tsx");
        const project = moduleGroupSource("pages/canvas/project.tsx");

        expect(editor).toContain("useResolvedCanvasResourceReferences");
        expect(panel).toContain("<LoaderCircle className=");
        expect(panel).toContain("animate-spin motion-reduce:animate-none");
        expect(panel).not.toContain("isRunning ? theme.accent.danger");
        expect(configComposer).toContain("CanvasResourceMentionTextarea");
        expect(configComposer).toContain("composerReferences(inputs, skillReferences)");
        expect(configComposer).not.toContain("contentEditable");
        expect(project).not.toContain("removeCanvasResourceMention");
    });

    test("keeps editable mention prompts separate from normalized generation prompts", () => {
        const imageExecutor = source("../src/pages/canvas/canvas-image-generation-executor.ts");
        const mediaExecutors = source("../src/pages/canvas/canvas-media-generation-executors.ts");
        const textExecutor = source("../src/pages/canvas/canvas-text-generation-executor.ts");
        const generationExecutor = source("../src/pages/canvas/use-canvas-generation-executor.ts");

        expect(imageExecutor.match(/canvasGenerationPromptMetadata\(prompt, effectivePrompt\)/g)?.length).toBeGreaterThanOrEqual(3);
        expect(imageExecutor).toContain("imageBatchExpanded: count > 1 ? true : undefined");
        expect(imageExecutor).toContain("imageGenerationReferenceConnections");
        expect(imageExecutor).toContain("retireImageBatchChildren");
        expect(mediaExecutors.match(/canvasGenerationPromptMetadata\(prompt, effectivePrompt\)/g)?.length).toBe(2);
        expect(textExecutor.match(/canvasGenerationPromptMetadata\(prompt, effectivePrompt\)/g)?.length).toBe(2);
        expect(generationExecutor).toContain("composerContent: prompt");
        expect(generationExecutor).toContain("canvasGenerationPromptMetadata(prompt, statusPrompt)");
    });

    test("anchors the mention menu to the caret instead of the textarea edge", () => {
        const component = moduleGroupSource("components/canvas/canvas-resource-mention-textarea.tsx");

        expect(component).toContain("cursorOffset={mention.end}");
        expect(component).toContain("mentionCaretRect(anchor, cursorOffset)");
        expect(component).toContain("textareaCaretRect(anchor, cursorOffset)");
        expect(component).toContain('transform: position.showAbove ? "translateY(-100%)" : undefined');
        expect(component).toContain('window.addEventListener("scroll", updatePosition, true)');
    });

    test("renders skill references as descriptive workflow rows", () => {
        const component = moduleGroupSource("components/canvas/canvas-resource-mention-textarea.tsx");
        const css = source("../src/styles/globals.css");

        expect(component).toContain('reference.kind === "skill" ? "is-skill" : ""');
        expect(component).toContain("reference.skill?.description");
        expect(component).toContain("reference.skill?.fileCount");
        expect(component).toContain("<Workflow aria-hidden />");
        expect(css).toContain(".canvas-resource-mention-item.is-skill");
        expect(css).toContain(".canvas-resource-mention-meta");
    });

    test("skill chips use one colored icon instead of exposing the serialized token", () => {
        const component = moduleGroupSource("components/canvas/canvas-resource-mention-textarea.tsx");

        expect(component).toContain('const isDecorated = reference.kind === "skill" || reference.kind === "tool"');
        expect(component).toContain('chip.className = `canvas-resource-inline-mention ${isDecorated ? `is-${reference.kind}` : ""}`');
        expect(component).toMatch(/if \(reference.kind === "skill"\)\s*\{\s*prefix.innerHTML = inlineIconSvg\(Sparkles\)/);
        expect(component).toContain('prefix.textContent = "@"');
        expect(component).toContain("if (!isDecorated) chip.appendChild(createInlinePreview(reference));");
        expect(component).toContain('chip.style.setProperty("--canvas-skill-mention-color", skillMentionColor(reference))');
    });
});
