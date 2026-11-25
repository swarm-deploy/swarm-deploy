import { createApp } from "vue";
import { createPinia } from "pinia";

import App from "./App.vue";
import { i18n } from "./i18n";
import { router } from "./router";
import { useConfigStore } from "./stores/config";
import "./style.css";

if (/\bAndroid\b/i.test(navigator.userAgent) && window.matchMedia("(display-mode: standalone)").matches) {
  const viewport = document.querySelector<HTMLMetaElement>('meta[name="viewport"]');
  if (viewport) {
    viewport.content += ", viewport-fit=cover";
  }
}

const app = createApp(App);
const pinia = createPinia();
app.use(pinia);
void useConfigStore(pinia).loadLogLinkFields();
app.use(router);
app.use(i18n);
app.mount("#app");
