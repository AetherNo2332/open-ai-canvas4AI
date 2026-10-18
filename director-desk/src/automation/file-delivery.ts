import { removedCapabilityMessage } from '../host/desktop-removed.ts';

/**
 * 云端:桌面版按路径读写已剥离。保留调用面与类型,运行时给出显式不可用错误,
 * 由调用方的错误处理呈现给用户(不静默失败)。
 */
export interface FileDestination { path?: string; directory?: string; filename?: string; overwrite?: boolean }

export async function desktopFile(_action: string, _data?: unknown): Promise<any> {
    throw Error(removedCapabilityMessage('local-path'));
}

export async function openOutput(_destination: FileDestination, _name: string, _kind: string): Promise<any> {
    throw Error(removedCapabilityMessage('local-path'));
}
