// Generated from Go canvas command/task contracts. From backend: go run ./internal/canvas/commands/generate
export const CANVAS_COMMAND_PROTOCOL = 1 as const;
export type OperationType =
    | "add_node"
    | "update_node"
    | "delete_node"
    | "duplicate_node"
    | "select_node_version"
    | "set_parent"
    | "connect_nodes"
    | "delete_connection"
    | "update_connection"
    | "replace_text"
    | "reorder_nodes"
    | "reorder_connections"
    | "reorder_rows"
    | "edit_drawing"
    | "edit_storyboard"
    | "bind_storyboard_output"
    | "edit_batch_table"
    | "create_previs_scene"
    | "edit_previs_scene"
    | "delete_previs_scene"
    | "bind_asset"
    | "rename_canvas"
    | "delete_canvas"
    | "edit_canvas_settings"
    | "edit_timeline"
    | "migrate_references";

export interface ChangeSet {
    entities: Array<EntityChange>;
    fields?: Record<string, FieldChange>;
    nodeOrder?: Array<string>;
    connectionOrder?: Array<string>;
    recreatedEntities?: Record<string, boolean>;
}

export interface Command {
    protocolVersion: 1;
    canvasId: string;
    documentEpoch: string;
    operationId: string;
    clientId: string;
    baseRevision: number;
    expectedEntityVersions?: Record<string, number>;
    operations: Array<Operation>;
}

export interface Consumer {
    kind?: string;
    rowId?: string;
    columnId?: string;
    field?: string;
    outputNodeId?: string;
}

export interface CreateCanvasRequest {
    operationId: string;
    clientId: string;
    title: string;
    projectId?: string;
    initialOperations?: Array<Operation>;
}

export interface DrawingOperation {
    type: string;
    id: string;
    record?: Record<string, unknown>;
}

export interface EntityChange {
    key: string;
    value?: Record<string, unknown>;
    delete?: boolean;
}

export interface FieldBaseline {
    exists: boolean;
    value: unknown;
}

export interface FieldChange {
    value?: unknown;
    delete?: boolean;
}

export interface MaterializationStatus {
    taskId: string;
    state: string;
    outcome?: string;
    message?: string;
    attempts: number;
    nextAttemptAt?: string;
}

export interface MigrationConflict {
    scopeKey: string;
    fieldNodeIds: Array<string>;
    edgeNodeIds: Array<string>;
}

export interface MigrationPreview {
    required: boolean;
    conflicts: Array<MigrationConflict>;
}

export interface NodeFieldBaselines {
    nodeType: string;
    generation?: number;
    workflowKind?: string;
    fields: Record<string, FieldBaseline>;
}

export interface Operation {
    type: OperationType;
    id: string;
    nodeType?: string;
    title?: string;
    content?: string;
    patch?: Record<string, unknown>;
    fieldBaselines?: NodeFieldBaselines;
    x?: number;
    y?: number;
    fromNodeId?: string;
    toNodeId?: string;
    sourceNodeId?: string;
    parentId?: string;
    match?: string;
    replacement?: string;
    nodeIds?: Array<string>;
    rowIds?: Array<string>;
    fromHandleId?: string;
    toHandleId?: string;
    fromAnchorRatio?: number;
    toAnchorRatio?: number;
    relation?: string;
    storyboardRowId?: string;
    engine?: string;
    drawingOperations?: Array<DrawingOperation>;
    drawingId?: string;
    drawingSnapshot?: Record<string, unknown>;
    drawingElementIds?: Array<string>;
    drawingOptions?: Record<string, unknown>;
    action?: string;
    rowId?: string;
    outputMode?: string;
    columnId?: string;
    batchOperation?: string;
    concurrency?: number;
    globalPrompt?: string;
    templateId?: string;
    previsOperations?: Array<PrevisOperation>;
    previsEntityOperations?: Array<PrevisEntityOperation>;
    assetId?: string;
    timelineOperations?: Array<TimelineOperation>;
}

export interface PrevisEntityOperation {
    type: string;
    id?: string;
    patch?: Record<string, unknown>;
    ids?: Array<string>;
}

export interface PrevisOperation {
    type: string;
    id: string;
    name?: string | null;
    title?: string | null;
    kind: string;
    primitive: string;
    archetype: string;
    color?: string | null;
    parentId?: string | null;
    visible?: boolean | null;
    pose: string;
    characterAssetId?: string | null;
    characterVersionId?: string | null;
    referenceNodeId?: string | null;
    characterName?: string | null;
    position?: Array<number> | null;
    rotation?: Array<number> | null;
    scale?: Array<number> | null;
    target?: Array<number> | null;
    keyframeId?: string | null;
    time?: number | null;
    easing: string;
    pathPoints: Array<Array<number>>;
    pathSpeed: string;
    startDelay?: number | null;
    orientToPath?: boolean | null;
    cameraId?: string | null;
    duration?: number | null;
    fps?: number | null;
    shotSize: string;
    cameraMove: string;
    prompt?: string | null;
    focalLength?: number | null;
    lightType?: string | null;
    intensity?: number | null;
    angle?: number | null;
    penumbra?: number | null;
    castShadow?: boolean | null;
    background?: string | null;
    environmentIntensity?: number | null;
    gridVisible?: boolean | null;
    activeShotId?: string | null;
}

export interface PrevisSubmitRequest {
    command: Command;
    sceneId: string;
    shotId: string;
    options?: Record<string, unknown>;
}

export interface Receipt {
    protocolVersion: 1;
    canvasId: string;
    documentEpoch: string;
    operationId: string;
    clientId: string;
    actorId: string;
    revision: number;
    eventSeq: number;
    changes: ChangeSet;
    entityVersions: Record<string, number>;
    entityGenerations?: Record<string, number>;
    reset?: Snapshot;
}

export interface Snapshot {
    protocolVersion: 1;
    canvasId: string;
    documentEpoch: string;
    revision: number;
    eventSeq: number;
    document: Record<string, unknown>;
    entityVersions: Record<string, number>;
    entityGenerations?: Record<string, number>;
    referenceMigration?: MigrationPreview;
}

export interface SubmitRequest {
    command: Command;
    targetNodeId: string;
    task: TaskRequest;
    consumer?: Consumer;
}

export interface TaskHandle {
    taskId: string;
    receipt: Receipt;
}

export interface TaskRequest {
    mode: string;
    type?: string;
    operation?: string;
    prompt: string;
    logicalModelId?: string;
    input?: Record<string, unknown>;
}

export interface TimelineOperation {
    type: string;
    id?: string;
    patch?: Record<string, unknown>;
    trackIds?: Array<string>;
    clipIds?: Array<string>;
}
