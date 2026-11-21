<script setup lang="ts">
import { computed } from "vue";

import type { NetworkInfo, SwarmService } from "../../api/types";
import AppCopyButton from "../common/AppCopyButton.vue";
import AppModal from "../common/AppModal.vue";
import AppTable from "../common/AppTable.vue";

const props = defineProps<{
  target: string;
  network?: NetworkInfo;
  services: SwarmService[] | null;
  loading: boolean;
  error: string;
}>();
const emit = defineEmits<{ close: [] }>();
const connected = computed(() => (props.services ?? []).filter((service) =>
  service.network_targets?.some((target) => target === props.target ||
    target === props.network?.id || target === props.network?.name),
).sort((left, right) => left.full_name.localeCompare(right.full_name)));
</script>

<template>
  <AppModal open labelledby="network-details-title" card-class="network-details-modal" @close="emit('close')">
    <template #title><h2 id="network-details-title">Network details</h2></template>
    <div class="modal-body">
      <p v-if="loading" class="meta" role="status">Loading network details...</p>
      <p v-else-if="error" class="error" role="alert">{{ error }}</p>
      <p v-else-if="!network" class="meta">Network details are unavailable.</p>
      <AppTable summary fixed aria-label="Network metadata">
        <tr v-if="network">
          <th scope="row">ID</th>
          <td><code>{{ network.id }}</code> <AppCopyButton :value="network.id" color="var(--text)" label="Copy network ID" /></td>
        </tr>
        <tr><th scope="row">{{ network ? "Name" : "Target" }}</th><td>{{ network?.name || target }}</td></tr>
        <template v-if="network">
          <tr><th scope="row">Scope</th><td>{{ network.scope || "n/a" }}</td></tr>
          <tr><th scope="row">Driver</th><td>{{ network.driver || "n/a" }}</td></tr>
          <tr><th scope="row">Attachable</th><td>{{ network.attachable ? "Yes" : "No" }}</td></tr>
          <tr><th scope="row">Internal</th><td>{{ network.internal ? "Yes" : "No" }}</td></tr>
          <tr><th scope="row">Ingress</th><td>{{ network.ingress ? "Yes" : "No" }}</td></tr>
          <template v-for="(pool, index) in network.ipam" :key="index">
            <tr v-if="pool.subnet"><th scope="row">Subnet</th><td>{{ pool.subnet }}</td></tr>
            <tr v-if="pool.gateway"><th scope="row">Gateway</th><td>{{ pool.gateway }}</td></tr>
          </template>
        </template>
        <tr>
          <th scope="row">Connected services</th>
          <td>
            <ul v-if="connected.length" class="network-services">
              <li v-for="service in connected" :key="service.id">
                <RouterLink class="assistant-md-link" :to="{ name: 'realtime', query: { stack: service.stack, service: service.name } }">{{ service.full_name }}</RouterLink>
                <span class="meta">{{ service.replicas.running }} / {{ service.replicas.desired }} tasks</span>
              </li>
            </ul>
            <span v-else>{{ services && services.every((service) => service.network_targets !== undefined) ? "No connected services." : "Unavailable" }}</span>
          </td>
        </tr>
      </AppTable>
    </div>
  </AppModal>
</template>

<style scoped>
.network-details-modal { width: min(560px, calc(100vw - 24px)); }
.network-services { display: grid; gap: 4px; padding: 0; margin: 0; list-style: none; }
.network-services li { display: flex; justify-content: space-between; flex-wrap: wrap; gap: 4px 10px; }
</style>
