import { Input } from "antd";
import { useEffect, useState } from "react";

import { AppModal } from "@/components/ui/product/app-modal";
import { translate } from "@/i18n";

/** submap 标题栏与右键菜单共用的模板命名弹窗。 */
export function CanvasSubmapSavePresetModal({ open, initialTitle, onCancel, onSave }: { open: boolean; initialTitle: string; onCancel: () => void; onSave: (name: string) => void }) {
    const [name, setName] = useState(initialTitle);

    useEffect(() => {
        if (open) setName(initialTitle);
    }, [initialTitle, open]);

    const commit = () => {
        const trimmed = name.trim();
        if (trimmed) onSave(trimmed);
    };

    return (
        <AppModal
            open={open}
            title={translate("canvasPage.nodeOps.submapPresetSaveAs")}
            okText={translate("canvasPage.nodeOps.submapPresetSave")}
            cancelText={translate("common.cancel")}
            okButtonProps={{ disabled: !name.trim() }}
            onOk={commit}
            onCancel={onCancel}
            destroyOnHidden
        >
            <p className="mb-3 text-sm opacity-70">{translate("canvasPage.nodeOps.submapPresetSaveAsDetail")}</p>
            <Input autoFocus value={name} maxLength={80} placeholder={translate("canvasPage.nodeOps.submapPresetNamePlaceholder")} onChange={(event) => setName(event.target.value)} onPressEnter={commit} />
        </AppModal>
    );
}
