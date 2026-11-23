import { createApp } from "vue";
import { createPinia } from "pinia";

import App from "./App.vue";
import { i18n } from "./i18n";
import { router } from "./router";
import { useConfigStore } from "./stores/config";
import "./style.css";

const app = createApp(App);
const pinia = createPinia();
app.use(pinia);
void useConfigStore(pinia).loadLogLinkFields();
app.use(router);
app.use(i18n);
app.mount("#app");
