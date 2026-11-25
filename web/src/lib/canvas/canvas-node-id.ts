/**
 * 画布定位 ID 的顺序分配器：所有 agent 可读取并需要回传的定位键
 * （节点、连线、批量表行列、导演台场景/镜头/对象）一律使用
 * "{prefix}-{N}" 形式，杜绝随机字母数字串，保证 LLM 可读。
 *
 * 「不复用已删除的编号」这条承诺靠高水位计数器兑现，而不是靠扫描当前还活着的对象：
 * 删掉最大编号后若只从存活集合求最大值，下一个分配就会把那个号还回来，而模型跨轮
 * 上下文里的旧回指（metadata.batchRowId 等）就会静默指向新对象。所以计数器只增不减。
 */

/** prefix → 已分配到的最大序号。只增不减；删除对象不回收编号。 */
export type SequentialCounters = Record<string, number>;

const sequentialIdPattern = /^(.*)-(\d+)$/;

function splitSequentialId(id: string): { prefix: string; index: number } | null {
    const match = sequentialIdPattern.exec(id);
    if (!match || !match[1]) return null;
    return { prefix: match[1], index: Number(match[2]) };
}

function sequentialIndex(id: string, prefix: string): number | null {
    const rest = id.slice(prefix.length + 1);
    if (!id.startsWith(`${prefix}-`) || !/^\d+$/.test(rest)) return null;
    return Number(rest);
}

/** 返回 prefix 下一个未占用的 "{prefix}-{N}" 顺序 ID；本函数只读 counters。 */
export function nextSequentialId(taken: Iterable<string>, prefix: string, counters?: SequentialCounters): string {
    let max = counters?.[prefix] ?? 0;
    for (const id of taken) {
        const index = sequentialIndex(id, prefix);
        if (index !== null && index > max) max = index;
    }
    return `${prefix}-${max + 1}`;
}

/** 从一批既有 id 求每个 prefix 的最大序号，作为存量画布的高水位起点。 */
export function seedSequentialCounters(ids: Iterable<string>): SequentialCounters {
    const counters: SequentialCounters = {};
    for (const id of ids) {
        const parsed = splitSequentialId(id);
        if (!parsed) continue;
        if (parsed.index > (counters[parsed.prefix] ?? 0)) counters[parsed.prefix] = parsed.index;
    }
    return counters;
}

/** 多份高水位逐 key 取 max；任何整体覆盖都可能把已经发出的编号还回去。 */
export function mergeSequentialCounters(...sources: Array<SequentialCounters | undefined>): SequentialCounters {
    const merged: SequentialCounters = {};
    for (const source of sources) {
        if (!source) continue;
        for (const [prefix, index] of Object.entries(source)) {
            if (!Number.isFinite(index)) continue;
            if (index > (merged[prefix] ?? 0)) merged[prefix] = index;
        }
    }
    return merged;
}

export type SequentialIdAllocator = ((prefix: string) => string) & {
    /** 登记调用方自带、不是本分配器产生的 ID。 */
    reserve(id: string): void;
    /** 与分配器共享引用的高水位；批量结束后整体写回项目文档。 */
    counters: SequentialCounters;
};

export type SequentialIdCanvas = {
    nodes: ReadonlyArray<{ id: string }>;
    connections: ReadonlyArray<{ id: string }>;
    idCounters?: SequentialCounters;
};

/** 「读文档 → 分配一个 → 返回推进后的高水位」的一步封装。 */
export function allocateSequentialId(canvas: SequentialIdCanvas, prefix: string, extraTaken: Iterable<string> = []): { id: string; counters: SequentialCounters } {
    const liveIds = [...canvas.nodes.map((node) => node.id), ...canvas.connections.map((connection) => connection.id), ...extraTaken];
    const counters = mergeSequentialCounters(seedSequentialCounters(liveIds), canvas.idCounters);
    const allocate = createSequentialIdAllocator(liveIds, counters);
    return { id: allocate(prefix), counters };
}

/**
 * 为一次批量发号准备起始状态。
 *
 * resetScopeId 用于 submap 子树整体重建：子节点 ID 是 `${scopeId}__${type}-N`
 * 的拼装，localId 才是语义身份。该类复合前缀的旧存活 ID 与持久高水位都要排除，
 * 否则整体重建会让局部编号持续递增。
 */
export function prepareSequentialIdBatch(
    nodes: ReadonlyArray<{ id: string }>,
    connections: ReadonlyArray<{ id: string }>,
    extraTaken: Iterable<string>,
    persisted: SequentialCounters | undefined,
    resetScopeId?: string,
): { taken: string[]; counters: SequentialCounters } {
    const resetPrefix = resetScopeId ? `${resetScopeId}__` : null;
    const isResettable = (id: string) => resetPrefix !== null && id.startsWith(resetPrefix);
    const taken = [...nodes.map((node) => node.id), ...connections.map((connection) => connection.id), ...extraTaken].filter((id) => !isResettable(id));
    const base: SequentialCounters = {};
    if (persisted) {
        for (const [prefix, index] of Object.entries(persisted)) {
            if (!isResettable(prefix)) base[prefix] = index;
        }
    }
    return { taken, counters: mergeSequentialCounters(seedSequentialCounters(taken), base) };
}

/** 一次批量创建用的分配器：连续分配不撞号，并原地推进共享高水位。 */
export function createSequentialIdAllocator(taken: Iterable<string>, counters: SequentialCounters = {}): SequentialIdAllocator {
    const used = new Set(taken);
    const allocate = (prefix: string): string => {
        let max = counters[prefix] ?? 0;
        for (const id of used) {
            const index = sequentialIndex(id, prefix);
            if (index !== null && index > max) max = index;
        }
        const index = max + 1;
        counters[prefix] = index;
        const id = `${prefix}-${index}`;
        used.add(id);
        return id;
    };
    const allocator = allocate as SequentialIdAllocator;
    allocator.reserve = (id: string) => {
        used.add(id);
        const parsed = splitSequentialId(id);
        if (parsed && parsed.index > (counters[parsed.prefix] ?? 0)) counters[parsed.prefix] = parsed.index;
    };
    allocator.counters = counters;
    return allocator;
}
