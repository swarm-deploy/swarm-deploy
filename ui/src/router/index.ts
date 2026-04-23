import { createRouter, createWebHistory } from "vue-router";

import ServicesView from "../views/ApplicationsView.vue";
import ClusterView from "../views/ClusterView.vue";
import OverviewView from "../views/OverviewView.vue";
import SecretsView from "../views/SecretsView.vue";
import ServiceView from "../views/ServiceView.vue";

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
      path: "/services/:stack/:service",
      name: "service-details",
      component: ServiceView,
    },
    {
      path: "/cluster",
      name: "cluster",
      component: ClusterView,
    },
    {
      path: "/secrets",
      name: "secrets",
      component: SecretsView,
    },
  ],
});
