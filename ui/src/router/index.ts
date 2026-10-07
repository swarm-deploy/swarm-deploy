import { createRouter, createWebHistory } from "vue-router";

import ServicesView from "../views/ApplicationsView.vue";
import AlertsView from "../views/AlertsView.vue";
import ClusterView from "../views/ClusterView.vue";
import EventsView from "../views/EventsView.vue";
import GraphView from "../views/GraphView.vue";
import NetworksView from "../views/NetworksView.vue";
import OverviewView from "../views/OverviewView.vue";
import RecommendationsView from "../views/RecommendationsView.vue";
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
      path: "/graph",
      name: "graph",
      component: GraphView,
    },
    {
      path: "/alerts",
      name: "alerts",
      component: AlertsView,
    },
    {
      path: "/events",
      name: "events",
      component: EventsView,
    },
    {
      path: "/recommendations",
      name: "recommendations",
      component: RecommendationsView,
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
      path: "/networks",
      name: "networks",
      component: NetworksView,
    },
    {
      path: "/secrets",
      name: "secrets",
      component: SecretsView,
    },
  ],
});
