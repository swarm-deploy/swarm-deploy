import { createRouter, createWebHistory } from "vue-router";

import ServicesView from "../views/ApplicationsView.vue";
import ClusterView from "../views/ClusterView.vue";
import EventsView from "../views/EventsView.vue";
import OverviewView from "../views/OverviewView.vue";
import SecretsView from "../views/SecretsView.vue";

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/",
      redirect: "/overview",
    },
    {
      path: "/overview",
      name: "overview",
      component: OverviewView,
    },
    {
      path: "/services",
      name: "services",
      component: ServicesView,
    },
    {
      path: "/cluster",
      name: "cluster",
      component: ClusterView,
    },
    {
      path: "/events",
      name: "events",
      component: EventsView,
    },
    {
      path: "/secrets",
      name: "secrets",
      component: SecretsView,
    },
  ],
});
