<script setup lang="ts">
import { computed, onMounted } from "vue";

import ServicesTable from "../components/overview/ServicesTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import { useOverviewStore } from "../stores/overview";

const overviewStore = useOverviewStore();

const servicesByStack = computed(() => {
  const grouped = new Map<string, typeof overviewStore.services>();

  for (const stack of overviewStore.stacks) {
    grouped.set(stack.name, []);
  }

  for (const service of overviewStore.services) {
    if (!grouped.has(service.stack)) {
      grouped.set(service.stack, []);
    }

    grouped.get(service.stack)?.push(service);
  }

  return Array.from(grouped.entries())
    .map(([stackName, services]) => ({
      stackName,
      services: [...services].sort((left, right) => left.name.localeCompare(right.name)),
    }))
    .sort((left, right) => left.stackName.localeCompare(right.stackName));
});

onMounted(async () => {
  await overviewStore.loadOverview();
});
</script>

<template>
  <section class="services-page">
    <header class="services-header">
      <h2>Services</h2>
    </header>

    <AppTableEmpty
      v-if="overviewStore.loading && overviewStore.stacks.length === 0 && overviewStore.services.length === 0"
      message="Loading..."
    />

    <AppTableEmpty v-else-if="overviewStore.loadingError && servicesByStack.length === 0">
      Failed to load services: {{ overviewStore.loadingError }}
    </AppTableEmpty>

    <AppTableEmpty v-else-if="servicesByStack.length === 0" message="No services captured yet." />

    <div v-else class="stack-dropdown-list">
      <details v-for="group in servicesByStack" :key="group.stackName" class="stack-dropdown" open>
        <summary class="stack-summary">
          <span class="stack-summary-heading">
            <svg class="stack-summary-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z" />
              <path d="m22 12.5-9.17 4.17a2 2 0 0 1-1.66 0L2 12.5" />
              <path d="m22 17.5-9.17 4.17a2 2 0 0 1-1.66 0L2 17.5" />
            </svg>
            <span class="stack-summary-title">{{ group.stackName }}</span>
          </span>
          <span class="stack-summary-meta">{{ group.services.length }} services</span>
          <span class="stack-summary-chevron" aria-hidden="true">▾</span>
        </summary>

        <ServicesTable :services="group.services" />
      </details>
    </div>
  </section>
</template>
