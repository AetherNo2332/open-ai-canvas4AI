import { Gift } from "lucide-react";
import { cn } from "@/lib/utils";

export function WorkspaceCreditGiftMark({ className }: { className?: string }) {
    return <span className={cn("app-workspace-credit-gift", className)} aria-hidden="true"><Gift /></span>;
}
