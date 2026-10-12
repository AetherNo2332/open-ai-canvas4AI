import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
    plugins: [react()],
    resolve: { alias: { "@": resolve(import.meta.dirname, "src") } },
    build: { outDir: "dist-previs", rolldownOptions: { input: resolve(import.meta.dirname, "previs-render.html") } },
});
