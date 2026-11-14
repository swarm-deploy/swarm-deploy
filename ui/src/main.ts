import { createApp } from "vue";
import { createPinia } from "pinia";

import App from "./App.vue";
import { router } from "./router";
import { useConfigStore } from "./stores/config";
import "./style.css";

const app = createApp(App);
const pinia = createPinia();
app.use(pinia);
void useConfigStore(pinia).loadLogLinkFields();
app.use(router);
app.mount("#app");
