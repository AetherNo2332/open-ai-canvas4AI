import React from "react";
import { createRoot } from "react-dom/client";
import "antd/dist/reset.css";
import "./styles/globals.css";
import "./styles/shared/model-picker.css";
import "./styles/shared/overlays.css";
import "./styles/shared/scrollbars.css";
import "./styles/home-skin.css";
// 全局自举内置插件注册（editor-shell 等预设以模块副作用注册编辑器插槽）：
// 冷启动直达编辑器时素材/时间线等插槽不再为空。
import "@/lib/plugins/builtin";
import { RouterProvider } from "react-router";

import { AppProviders } from "@/components/layout/app-providers";
import { router } from "@/router";

// body 字体随 globals.css 的 --font-home（Mona Sans + 思源黑体）统一；此处内联写入
// 是为了让首帧在样式表解析前就持有最终字体栈，避免冷启动闪换字体。
document.body.style.fontFamily = '"Mona Sans","Source Han Sans SC","Microsoft YaHei",system-ui,sans-serif';

createRoot(document.getElementById("root")!).render(
    <React.StrictMode>
        <AppProviders>
            <RouterProvider router={router} />
        </AppProviders>
    </React.StrictMode>,
);
