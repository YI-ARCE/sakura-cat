import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [vue(), wails("./bindings")],
  build: {
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        // 将大依赖切为独立 vendor chunk，避免主入口 index 过大。
        // 登录窗只加载 index + Login + naive-vendor，不必背全部依赖。
        // 注意：本项目用 Vite8 + Rolldown，manualChunks 仅支持函数形式。
        manualChunks(id) {
          if (id.includes("node_modules")) {
            if (id.includes("naive-ui")) return "naive-ui"
            if (
              id.includes("/vue/") ||
              id.includes("/vue-router/") ||
              id.includes("/pinia/") ||
              id.includes("@vue/")
            ) {
              return "vue-vendor"
            }
          }
        },
      },
    },
  },
});
