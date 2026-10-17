export default {
  // 由 nginx 同源托管在 /desk/(见仓库根 nginx.conf);资源 URL 必须带该前缀。
  base: '/desk/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // three.js 等运行时体量大,单独的 chunk 体积告警阈值放宽,避免噪声掩盖真实问题。
    chunkSizeWarningLimit: 4096,
  },
  server: {
    watch: {
      // Build outputs and isolated test profiles are not application source.
      ignored: ['**/tmp/**', '**/.audit/**', '**/release/**', '**/.local/**'],
    },
  },
};
