<script setup lang="ts">
import { computed, onMounted } from "vue";

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

    <div
      v-if="overviewStore.loading && overviewStore.stacks.length === 0 && overviewStore.services.length === 0"
      class="services-empty"
    >
      <p class="meta">Loading...</p>
    </div>

    <div v-else-if="overviewStore.loadingError && servicesByStack.length === 0" class="services-empty">
      <p class="meta">Failed to load services: {{ overviewStore.loadingError }}</p>
    </div>

    <div v-else-if="servicesByStack.length === 0" class="services-empty">
      <p class="meta">No services captured yet.</p>
    </div>

    <div v-else class="stack-dropdown-list">
      <details v-for="group in servicesByStack" :key="group.stackName" class="stack-dropdown" open>
        <summary class="stack-summary">
          <span class="stack-summary-title">{{ group.stackName }}</span>
          <span class="stack-summary-meta">{{ group.services.length }} services</span>
          <span class="stack-summary-chevron" aria-hidden="true">▾</span>
        </summary>

        <div class="stack-services-table-wrap">
          <table class="container-status-table services-stack-table">
            <colgroup>
              <col class="services-col-name" />
              <col class="services-col-type" />
              <col class="services-col-version" />
              <col class="services-col-actions" />
            </colgroup>
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Version</th>
                <th />
              </tr>
            </thead>
            <tbody>
              <tr v-if="group.services.length === 0">
                <td colspan="4" class="stack-service-empty-cell">No services captured yet.</td>
              </tr>
              <tr v-for="service in group.services" :key="`${group.stackName}-${service.name}`">
                <td class="services-cell-name" :title="service.name || undefined">
                  <strong class="stack-service-name">{{ service.name || "unknown" }}</strong>
                </td>
                <td class="services-cell-type">{{ service.type_title || service.type }}</td>
                <td class="services-cell-version" :title="service.image">{{ service.image_version || "—" }}</td>
                <td class="stack-service-actions-cell">
                  <button type="button" class="service-status-btn" @click="openServiceStatus(group.stackName, service.name)">
                    Details
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
    </div>
  </section>
</template>
