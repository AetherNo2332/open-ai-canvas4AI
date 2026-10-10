import builtinTraits from "../../../../backend/internal/canvas/connection/builtin.json";
import type { CanvasGenerationMode } from "@/types/canvas";

export type ConnectionTrait = {
    inputKind?: "text" | "image" | "video" | "audio" | "table_data";
    mode?: CanvasGenerationMode;
    metadataMode?: boolean;
    blocked?: boolean;
    acceptedInputKinds?: ("text" | "image" | "video" | "audio" | "table_data")[];
    acceptedSourceTypes?: string[];
    maxInputCount?: number;
};
export const connectionTraits = builtinTraits as Record<string, ConnectionTrait>;
