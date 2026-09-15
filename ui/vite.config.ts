import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

const appVersion = process.env.APP_VERSION?.trim() || "dev";
const buildTime = process.env.BUILD_TIME?.trim() || "";

export default defineConfig({
  plugins: [vue()],
  define: {
    __SWARM_DEPLOY_VERSION__: JSON.stringify(appVersion),
    __SWARM_DEPLOY_BUILD_TIME__: JSON.stringify(buildTime),
  },
  build: {
    outDir: "dist",
    assetsDir: "assets",
    emptyOutDir: true,
  },
});
