import { Input } from "antd";
import { useEffect, useRef, useState } from "react";

/** 长分镜表先展示内容，只有当前编辑格才挂载需要测高的输入控件。 */
export function StoryboardTextCell({ value, label, disabled, onChange }: { value: string; label: string; disabled?: boolean; onChange: (value: string) => void }) {
    const [editing, setEditing] = useState(false);
    const buttonRef = useRef<HTMLButtonElement>(null);
    const restoreFocus = useRef(false);
    useEffect(() => {
        if (disabled) setEditing(false);
    }, [disabled]);
    useEffect(() => {
        if (!editing && restoreFocus.current) {
            buttonRef.current?.focus({ preventScroll: true });
            restoreFocus.current = false;
        }
    }, [editing]);
    if (editing && !disabled) {
        return (
            <Input.TextArea
                autoFocus
                autoSize={{ minRows: 1, maxRows: 6 }}
                value={value}
                placeholder={`填写${label}`}
                aria-label={label}
                onChange={(event) => onChange(event.target.value)}
                onBlur={() => setEditing(false)}
                onKeyDown={(event) => {
                    if (event.key === "Escape" && !event.nativeEvent.isComposing) {
                        event.stopPropagation();
                        restoreFocus.current = true;
                        setEditing(false);
                    }
                }}
            />
        );
    }
    return (
        <button
            type="button"
            ref={buttonRef}
            aria-label={`编辑${label}`}
            title={value || `点击填写${label}`}
            disabled={disabled}
            className="block min-h-11 w-full rounded-[var(--input-radius)] px-1 py-1 text-left outline-none hover:bg-foreground/[0.025] focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default"
            onClick={() => setEditing(true)}
        >
            <span className={`line-clamp-3 whitespace-pre-wrap break-words ${value ? "" : "text-foreground/40"}`}>{value || `点击填写${label}`}</span>
        </button>
    );
}
