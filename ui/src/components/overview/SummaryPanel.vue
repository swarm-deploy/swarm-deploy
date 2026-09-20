<script setup lang="ts">
import type { RouteLocationRaw } from "vue-router";

import OverviewCardAction from "./OverviewCardAction.vue";

defineProps<{
  title: string;
  icon: "deployments" | "alerts" | "recommendations";
  to: RouteLocationRaw;
}>();
</script>

<template>
  <article class="overview-summary-panel">
    <header class="overview-summary-panel-header">
      <div class="overview-summary-panel-heading">
        <span class="overview-summary-panel-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none">
            <template v-if="icon === 'deployments'">
              <path d="M12 3v11m0 0 4-4m-4 4-4-4" />
              <path d="M5 17v3h14v-3" />
            </template>
            <template v-else-if="icon === 'alerts'">
              <path d="M9.5 19h5" />
              <path d="M18 9a6 6 0 0 0-12 0c0 6-2.5 6-2.5 8h17c0-2-2.5-2-2.5-8Z" />
            </template>
            <template v-else>
              <path d="M9 18h6m-5 3h4" />
              <path d="M8.2 15.2a7 7 0 1 1 7.6 0c-.6.4-.8.9-.8 1.8H9c0-.9-.2-1.4-.8-1.8Z" />
            </template>
          </svg>
        </span>
        <h2 class="overview-panel-title">{{ title }}</h2>
      </div>
      <OverviewCardAction :to="to" />
    </header>

    <div class="overview-summary-panel-content">
      <slot />
    </div>
  </article>
</template>
