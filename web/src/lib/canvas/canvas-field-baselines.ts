import type { FieldBaseline, Operation } from "./command-contract.generated";
import type { CanvasCapabilities } from "./canvas-editor-commands";

type Entity = Record<string, unknown>;
const own = (object: object, key: string) => Object.hasOwn(object, key);
const safe = (key: string) => !["__proto__", "prototype", "constructor"].includes(key);

export function canvasBaselineDescriptor(node: Entity, capabilities: CanvasCapabilities) {
    const metadata = node.metadata as Entity | undefined;
    return capabilities.nodes.find((descriptor) => descriptor.baseType === node.type && descriptor.workflowKind === metadata?.workflowKind) || capabilities.nodes.find((descriptor) => descriptor.type === node.type);
}

/** Presence is part of the observation: absent and explicit null differ. */
export function canvasBaselineValue(node: Entity, path: string): FieldBaseline {
    let value: unknown = node;
    for (const key of path.split(".")) {
        if (!safe(key) || !value || typeof value !== "object" || !own(value, key)) return { exists: false, value: null };
        value = (value as Entity)[key];
    }
    return value === undefined ? { exists: false, value: null } : { exists: true, value: structuredClone(value) };
}

/** Used only on cloned local comparison documents, never on the wire. */
export function restoreCanvasBaselineValue(node: Entity, path: string, observed: FieldBaseline) {
    const parts = path.split(".");
    if (parts.some((key) => !safe(key))) return;
    let parent = node;
    for (const key of parts.slice(0, -1)) {
        if (!parent[key] || typeof parent[key] !== "object" || Array.isArray(parent[key])) {
            if (!observed.exists) return;
            parent[key] = {};
        }
        parent = parent[key] as Entity;
    }
    const key = parts.at(-1)!;
    if (observed.exists) Object.defineProperty(parent, key, { value: structuredClone(observed.value), enumerable: true, writable: true, configurable: true });
    else delete parent[key];
}

/** Guard the registry's semantic mirror paths, not unrelated task/display data. */
export function canvasBaselineAliases(node: Entity, aliases: string[], capabilities: CanvasCapabilities): string[] {
    const fields = canvasBaselineDescriptor(node, capabilities)?.patchFields;
    if (!fields || aliases.some((alias) => !safe(alias) || !own(fields, alias))) return [];
    const paths = new Set(aliases.map((alias) => fields[alias].path));
    const textEdit = ["metadata.content", "metadata.composerContent", "metadata.richText"].some((path) => paths.has(path));
    if (textEdit) {
        for (const path of ["metadata.content", "metadata.composerContent", "metadata.richText"]) paths.add(path);
    }
    for (const path of ["metadata.generationSpec", "metadata.inputBindings"]) {
        if (canvasBaselineValue(node, path).exists || (textEdit && paths.has("metadata.composerContent"))) paths.add(path);
    }
    return Object.entries(fields)
        .filter(([alias, field]) => safe(alias) && paths.has(field.path))
        .map(([alias]) => alias);
}

/** Assemble once from the original edit observation. A missing generation or
 * unsupported/structural batch keeps the existing entity CAS contract. */
export function withCanvasFieldBaselines(operations: Operation[], entities: Record<string, Entity>, generations: Record<string, number>, capabilities: CanvasCapabilities): Operation[] {
    if (operations.some((operation) => operation.type !== "update_node")) return structuredClone(operations);
    return operations.map((operation) => {
        if (operation.fieldBaselines) return structuredClone(operation);
        const key = "node:" + operation.id,
            node = entities[key],
            generation = generations[key];
        if (!node || !own(generations, key) || !Number.isSafeInteger(generation) || generation < 0) return structuredClone(operation);
        const descriptor = canvasBaselineDescriptor(node, capabilities);
        const aliases = canvasBaselineAliases(node, Object.keys(operation.patch || {}), capabilities);
        if (!descriptor || !aliases.length || aliases.length > 128) return structuredClone(operation);
        const metadata = node.metadata as Entity | undefined;
        return {
            ...structuredClone(operation),
            fieldBaselines: {
                nodeType: String(node.type),
                workflowKind: typeof metadata?.workflowKind === "string" ? metadata.workflowKind : "",
                generation,
                fields: Object.fromEntries(aliases.map((alias) => [alias, canvasBaselineValue(node, descriptor.patchFields[alias].path)])),
            },
        };
    });
}
