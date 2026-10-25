import { useCallback, useState } from "react";

import type { InsertAssetPayload } from "@/components/canvas/asset-picker-modal";
import type { CanvasNodeData, Position } from "@/types/canvas";

type CanvasCharacterEntryOptions = {
    projectId?: string;
    shortDramaEnabled: boolean;
    openProjectAssets: (category: string, position?: Position) => void;
    handleProjectAssetsInsert: (payloads: InsertAssetPayload[], position?: Position) => Promise<CanvasNodeData[]>;
};

export function useCanvasCharacterEntry({ projectId, shortDramaEnabled, openProjectAssets, handleProjectAssetsInsert }: CanvasCharacterEntryOptions) {
    const [characterLibraryOpen, setCharacterLibraryOpen] = useState(false);
    const [insertPosition, setInsertPosition] = useState<Position | undefined>();
    const openCharacters = useCallback((position?: Position) => {
        if (shortDramaEnabled && projectId) {
            openProjectAssets("character", position);
            return;
        }
        setInsertPosition(position);
        setCharacterLibraryOpen(true);
    }, [openProjectAssets, projectId, shortDramaEnabled]);
    const closeCharacterLibrary = useCallback(() => {
        setCharacterLibraryOpen(false);
        setInsertPosition(undefined);
    }, []);
    const insertLibraryCharacters = useCallback(async (payloads: InsertAssetPayload[]) => {
        await handleProjectAssetsInsert(payloads, insertPosition);
        setInsertPosition(undefined);
    }, [handleProjectAssetsInsert, insertPosition]);

    return {
        characterLibraryOpen,
        openCharacters,
        closeCharacterLibrary,
        insertLibraryCharacters,
    };
}
