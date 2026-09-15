/// <reference types="vite/client" />

declare const __SWARM_DEPLOY_VERSION__: string;
declare const __SWARM_DEPLOY_BUILD_TIME__: string;

declare module "*.vue" {
  import type { DefineComponent } from "vue";
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>;
  export default component;
}
