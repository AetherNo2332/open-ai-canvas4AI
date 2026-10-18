import type { Project, ProductionData, PromptMode } from '../model.ts';
import type { ZipEntry } from './zip.ts';
import { safeFilename } from './notes.ts';

/** Export authored text verbatim; notes alone are not a finished generation prompt. */
export const promptModes: readonly PromptMode[] = ['reference-video', 'text-only'];
export const promptModeLabel = (mode: PromptMode) => mode === 'text-only' ? '纯文本' : '参考视频';
export const promptField = (mode: PromptMode) => mode === 'text-only' ? 'textOnlyPrompt' : 'promptText';
export const selectedPromptMode = (data?: ProductionData): PromptMode => data?.promptMode ?? 'reference-video';
export function scenePromptFile(project: Pick<Project, 'name' | 'production'>, sceneName = project.name, mode = selectedPromptMode(project.production)): ZipEntry | undefined {
    const text = project.production?.[promptField(mode)];
    if (!text?.trim()) return undefined;
    return { name: `${safeFilename(sceneName)}-${mode === 'text-only' ? '纯文本提示词' : '视频提示词'}.txt`, data: new Blob(['\uFEFF', text], { type: 'text/plain;charset=utf-8' }) };
}
