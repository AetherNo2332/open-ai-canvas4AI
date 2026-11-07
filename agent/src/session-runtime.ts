import type { Model } from "@earendil-works/pi-ai";
import type { StreamFn } from "@earendil-works/pi-agent-core";
import {
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
  createAgentSession,
  type AgentSession,
  type InlineExtension,
  type SessionEntry,
} from "@earendil-works/pi-coding-agent";

export interface SessionRuntimeWorkspace {
  cwd: string;
  agentDir: string;
  cleanup: () => void;
}

export interface SessionRuntimeProvider {
  id: string;
  name: string;
  api: string;
  baseUrl: string;
  model: Model<any>;
  streamSimple: StreamFn;
  apiKey?: string;
}

export interface SessionRuntimeOptions {
  workspace: SessionRuntimeWorkspace;
  sessionId: string;
  entries?: SessionEntry[];
  systemPrompt?: string;
  provider: SessionRuntimeProvider;
  extensionFactories?: InlineExtension[];
  toolNames?: string[];
  additionalSkillPaths?: string[];
  verifySkills?: (loader: DefaultResourceLoader) => void;
  configureSettings?: (settingsManager: SettingsManager) => void;
}

export interface SessionRuntime {
  session: AgentSession;
  sessionManager: SessionManager;
  settingsManager: SettingsManager;
  cleanup: () => void;
}

/**
 * Creates one isolated Pi Session from a Go-owned entry tree and host-owned
 * provider/tool extensions. The runtime has no Canvas or business knowledge;
 * callers supply the model bridge, prompt and extension factories.
 */
export async function createSessionRuntime(options: SessionRuntimeOptions): Promise<SessionRuntime> {
  const { workspace, provider } = options;
  try {
    const runtime = await ModelRuntime.create({ modelsPath: null, refreshOnCreate: false, allowModelNetwork: false });
    const model = provider.model;
    runtime.registerProvider(provider.id, {
      name: provider.name,
      api: provider.api,
      baseUrl: provider.baseUrl,
      models: [{
        id: model.id, name: model.name, api: provider.api, reasoning: model.reasoning, input: model.input,
        cost: model.cost, contextWindow: model.contextWindow, maxTokens: model.maxTokens,
      }],
      streamSimple: provider.streamSimple,
    } as never);
    await runtime.setRuntimeApiKey(provider.id, provider.apiKey ?? "pi-session-runtime-internal");
    const resolved = runtime.getModel(provider.id, model.id);
    if (!resolved) throw new Error("session runtime provider model did not resolve");

    const settingsManager = SettingsManager.create(workspace.cwd, workspace.agentDir);
    settingsManager.setRetryEnabled(false);
    options.configureSettings?.(settingsManager);
    const resourceLoader = new DefaultResourceLoader({
      cwd: workspace.cwd,
      agentDir: workspace.agentDir,
      settingsManager,
      noExtensions: true,
      noSkills: true,
      noPromptTemplates: true,
      noThemes: true,
      noContextFiles: true,
      ...(options.additionalSkillPaths ? { additionalSkillPaths: options.additionalSkillPaths } : {}),
      extensionFactories: options.extensionFactories,
      systemPrompt: options.systemPrompt,
    });
    await resourceLoader.reload();
    options.verifySkills?.(resourceLoader);

    const sessionManager = SessionManager.inMemory(workspace.cwd, { id: options.sessionId }, options.entries);
    const { session } = await createAgentSession({
      cwd: workspace.cwd,
      agentDir: workspace.agentDir,
      model: resolved,
      modelRuntime: runtime,
      sessionManager,
      settingsManager,
      resourceLoader,
      noTools: "all",
      tools: options.toolNames ?? [],
    });
    return { session, sessionManager, settingsManager, cleanup: workspace.cleanup };
  } catch (error) {
    workspace.cleanup();
    throw error;
  }
}
