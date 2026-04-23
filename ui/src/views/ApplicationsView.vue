<script setup lang="ts">
import { onMounted } from "vue";

import { useOverviewStore } from "../stores/overview";

const overviewStore = useOverviewStore();

async function openServiceStatus(stackName: string, serviceName: string) {
  await overviewStore.openServiceStatusModal(stackName, serviceName);
}

onMounted(async () => {
  await overviewStore.loadOverview();
});
</script>

<template>
  <section class="services-page">
    <header class="services-header">
      <h2>Services</h2>
    </header>

    <div v-if="overviewStore.loading && overviewStore.stacks.length === 0" class="services-empty">
      <p class="meta">Loading...</p>
    </div>

    <div v-else-if="overviewStore.loadingError" class="services-empty">
      <p class="meta">Failed to load services: {{ overviewStore.loadingError }}</p>
    </div>

    <div v-else-if="overviewStore.stacks.length === 0" class="services-empty">
      <p class="meta">No stacks configured.</p>
    </div>

    <div v-else class="stack-dropdown-list">
      <details v-for="stack in overviewStore.stacks" :key="stack.name" class="stack-dropdown" open>
        <summary class="stack-summary">
          <span class="stack-summary-title">{{ stack.name }}</span>
          <span class="stack-summary-meta">{{ (stack.services || []).length }} services</span>
          <span class="stack-summary-chevron" aria-hidden="true">▾</span>
        </summary>

        <ul class="stack-services">
          <li v-if="!stack.services || stack.services.length === 0" class="stack-service-empty">
            No services captured yet.
          </li>
          <li v-for="service in stack.services || []" :key="`${stack.name}-${service.name}`" class="stack-service-item">
            <div class="stack-service-main">
              <strong>{{ service.name || "unknown" }}</strong>
              <span class="meta">{{ service.image || "unknown image" }}</span>
            </div>
            <div class="stack-service-actions">
              <button type="button" class="service-status-btn" @click="openServiceStatus(stack.name, service.name)">
                Status
              </button>
            </div>
          </li>
        </ul>
      </details>
    </div>
  </section>
</template>
