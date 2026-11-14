import { defineConfig } from "vitest/config";
import vue from "@vitejs/plugin-vue";

const appVersion = process.env.APP_VERSION?.trim() || "dev";
const buildTime = process.env.BUILD_TIME?.trim() || "";

export default defineConfig(({ command, mode }) => ({
  base: command === "build" ? "./" : "/",
  plugins: [vue()],
  test: {
    environment: "jsdom",
  },
  server: mode === "mock"
    ? {
        proxy: {
          "/api": {
            target: "http://127.0.0.1:4010",
            changeOrigin: true,
          },
        },
      }
    : undefined,
  define: {
    __SWARM_DEPLOY_VERSION__: JSON.stringify(appVersion),
    __SWARM_DEPLOY_BUILD_TIME__: JSON.stringify(buildTime),
  },
  build: {
    outDir: "dist",
    assetsDir: "assets",
    emptyOutDir: true,
  },
}));
