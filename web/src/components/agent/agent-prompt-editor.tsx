import { useEffect, useRef, type CSSProperties, type KeyboardEvent } from "react";
import type { AgentReference } from "@/lib/agent/reference";

export type AgentPromptEditorProps = {
    visible?: boolean;
    value: string;
    references?: AgentReference[];
    sendOnEnter?: boolean | "both";
    disabled?: boolean;
    onChange: (value: string) => void;
    onSubmit?: (event: { modifier: boolean; ctrlKey: boolean; metaKey: boolean }) => void;
    onKeyDown?: (event: KeyboardEvent<HTMLTextAreaElement>) => void;
    onContentSizeChange?: (height: number) => void;
    className?: string;
    containerClassName?: string;
    style?: CSSProperties;
    placeholder?: string;
    "aria-label"?: string;
    includeAssetLibrary?: boolean;
};

/** Accessible fallback editor for businesses without structured object references. */
export function AgentPromptEditor({ visible = true, value, sendOnEnter = "both", disabled, onChange, onSubmit, onKeyDown, onContentSizeChange, className, containerClassName, style, placeholder, "aria-label": ariaLabel }: AgentPromptEditorProps) {
    const ref = useRef<HTMLTextAreaElement>(null);

    useEffect(() => {
        const element = ref.current;
        if (!element) return;
        element.style.height = "0px";
        const height = element.scrollHeight;
        onContentSizeChange?.(height);
        element.style.height = "100%";
    }, [onContentSizeChange, value]);

    if (!visible) return null;
    return (
        <div className={containerClassName}>
            <textarea
                ref={ref}
                value={value}
                disabled={disabled}
                onChange={(event) => onChange(event.target.value)}
                onKeyDown={(event) => {
                    onKeyDown?.(event);
                    if (event.defaultPrevented || event.nativeEvent.isComposing || event.shiftKey) return;
                    if (event.key !== "Enter") return;
                    const modifier = event.ctrlKey || event.metaKey;
                    if (sendOnEnter === false && !modifier) return;
                    if (sendOnEnter !== "both" && sendOnEnter !== false && modifier) return;
                    event.preventDefault();
                    onSubmit?.({ modifier, ctrlKey: event.ctrlKey, metaKey: event.metaKey });
                }}
                className={className}
                style={style}
                placeholder={placeholder}
                aria-label={ariaLabel}
            />
        </div>
    );
}
