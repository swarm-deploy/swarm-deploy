<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { fetchDeploymentFilters, fetchDeployments } from "../api/overview";
import type { DeploymentFilterOptions, DeploymentSummary } from "../api/types";
import AppMultiSelect from "../components/common/AppMultiSelect.vue";
import AppPagination from "../components/common/AppPagination.vue";
import AppStatusBadge from "../components/common/AppStatusBadge.vue";
import AppTable from "../components/common/AppTable.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const store = useOverviewStore();
const route = useRoute();
const router = useRouter();
const selectedStacks = ref(normalizeQueryValues(route.query.stack_name));
const selectedServices = ref(normalizeQueryValues(route.query.service_name));
const stackOptions = ref<string[]>([]);
const serviceOptions = ref<DeploymentFilterOptions["services"]>([]);
const stackSelectOptions = computed(() => {
  const options = [...stackOptions.value];
  for (const stack of selectedStacks.value) {
    if (!options.includes(stack)) options.push(stack);
  }
  return options.sort((left, right) => left.localeCompare(right))
    .map((stack) => ({ value: stack, label: stack }));
});
const serviceSelectOptions = computed(() => {
  const services = serviceOptions.value.filter((service) => (
    selectedStacks.value.length === 0 || selectedStacks.value.includes(service.stack_name)
  ));
  const stacksByService = new Map<string, Set<string>>();
  for (const service of services) {
    const stacks = stacksByService.get(service.service_name) ?? new Set<string>();
    stacks.add(service.stack_name);
    stacksByService.set(service.service_name, stacks);
  }
  for (const service of selectedServices.value) {
    if (!stacksByService.has(service)) stacksByService.set(service, new Set());
  }
  return [...stacksByService.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([service, stacks]) => {
      const stackNames = [...stacks].sort((left, right) => left.localeCompare(right));
      return {
        value: service,
        label: service,
        description: selectedStacks.value.length === 1 ? undefined : stackNames.join(", "),
        searchText: `${service} ${stackNames.join(" ")}`,
      };
    });
});
const stackSelectionLabel = computed(() => selectionLabel(selectedStacks.value, "All stacks", "stacks"));
const serviceSelectionLabel = computed(() => selectionLabel(selectedServices.value, "All services", "services"));
const deployments = ref<DeploymentSummary[]>([]);
const error = ref("");
const loading = ref(false);
const nextCursor = ref("");
const cursors = ref<string[]>([""]);
const page = ref(0);
let request = 0;

function normalizeQueryValues(value: unknown): string[] {
  const values = Array.isArray(value) ? value : [value];
  return [...new Set(values.filter((item): item is string => typeof item === "string")
    .map((item) => item.trim()).filter(Boolean))];
}

function selectionLabel(values: string[], placeholder: string, noun: string): string {
  if (values.length === 0) return placeholder;
  if (values.length <= 2) return values.join(", ");
  return `${values.length} ${noun} selected`;
}

function sameValues(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function startedAtTimestamp(deployment: DeploymentSummary): number {
  const timestamp = Date.parse(deployment.started_at);
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

async function load(index: number, cursor: string) {
  const current = ++request;
  loading.value = true;
  error.value = "";
  try {
    const result = await fetchDeployments({
      stack: selectedStacks.value,
      serviceName: selectedServices.value,
      limit: 20,
      cursor: cursor || undefined,
    });
    if (current !== request) return;
    deployments.value = Array.isArray(result.deployments)
      ? [...result.deployments].sort((left, right) => startedAtTimestamp(right) - startedAtTimestamp(left))
      : [];
    nextCursor.value = result.next_cursor ?? "";
    page.value = index;
    cursors.value[index] = cursor;
  } catch (e) {
    if (current === request) error.value = e instanceof Error ? e.message : "Failed to load deployments";
  } finally {
    if (current === request) loading.value = false;
  }
}
function next() { if (nextCursor.value) void load(page.value + 1, nextCursor.value); }
function previous() { if (page.value > 0) void load(page.value - 1, cursors.value[page.value - 1] ?? ""); }

function selectStacks(stacks: string[]): void {
  const availableServices = new Set(serviceOptions.value
    .filter((service) => stacks.length === 0 || stacks.includes(service.stack_name))
    .map((service) => service.service_name));
  selectedStacks.value = stacks;
  selectedServices.value = selectedServices.value.filter((service) => availableServices.has(service));
  persistFilters();
}

function selectServices(services: string[]): void {
  selectedServices.value = services;
  persistFilters();
}

function persistFilters(): void {
  const query = { ...route.query };
  if (selectedStacks.value.length > 0) query.stack_name = [...selectedStacks.value];
  else delete query.stack_name;
  if (selectedServices.value.length > 0) query.service_name = [...selectedServices.value];
  else delete query.service_name;
  void router.replace({ query });
}

watch(() => [route.query.stack_name, route.query.service_name], ([stackValue, serviceValue]) => {
  const stacks = normalizeQueryValues(stackValue);
  const services = normalizeQueryValues(serviceValue);
  if (!sameValues(selectedStacks.value, stacks)) selectedStacks.value = stacks;
  if (!sameValues(selectedServices.value, services)) selectedServices.value = services;
});
watch([selectedStacks, selectedServices], () => {
  cursors.value = [""];
  nextCursor.value = "";
  void load(0, "");
});

function statusClass(status: unknown): string {
  switch (status) {
    case "succeeded": return "success";
    case "failed": return "failed";
    case "running": return "deployment-status-badge--running";
    default: return "unknown";
  }
}

function statusVariant(status: unknown): "success" | "error" | "info" | "neutral" {
  switch (status) {
    case "succeeded": return "success";
    case "failed": return "error";
    case "running": return "info";
    default: return "neutral";
  }
}

function statusLabel(status: unknown): string {
  const value = typeof status === "string" ? status.trim() : "";
  if (!value) return "Unknown";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function openDeployment(deploymentID: string): void {
  store.openDeploymentDetailsModal(deploymentID);
}

function openDeploymentFromKeyboard(event: KeyboardEvent, deploymentID: string): void {
  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
  event.preventDefault();
  openDeployment(deploymentID);
}

function openCommit(commit: string): void {
  void store.openCommitDetailsModal(commit);
}
onMounted(() => {
  void load(0, "");
  void fetchDeploymentFilters().then((options) => {
    stackOptions.value = Array.isArray(options.stacks) ? options.stacks : [];
    serviceOptions.value = Array.isArray(options.services) ? options.services : [];
  }).catch(() => { stackOptions.value = []; serviceOptions.value = []; });
});
</script>

<template>
  <section class="services-page">
    <header class="services-header"><h2>Deployments</h2></header>
    <p class="meta">Desired-state deployments and their execution attempts. Service convergence is not verified.</p>
    <div class="deployments-filter-bar"><div class="deployments-filter-field">
      <span>Stack</span>
      <AppMultiSelect
        :model-value="selectedStacks"
        :options="stackSelectOptions"
        label="Filter deployments by stack"
        placeholder="All stacks"
        heading="Stacks"
        :selection-label="stackSelectionLabel"
        searchable
        search-label="Search stacks"
        search-placeholder="Search stacks"
        empty-message="No matching stacks"
        @update:model-value="selectStacks"
      />
    </div>
    <div class="deployments-filter-field">
      <span>Service</span>
      <AppMultiSelect
        :model-value="selectedServices"
        :options="serviceSelectOptions"
        label="Filter deployments by service"
        placeholder="All services"
        heading="Services"
        :selection-label="serviceSelectionLabel"
        searchable
        search-label="Search services"
        search-placeholder="Search services"
        empty-message="No matching services"
        @update:model-value="selectServices"
      />
    </div></div>
    <p v-if="loading" role="status">Loading deployments...</p>
    <p v-else-if="error" role="alert">{{ error }} <button type="button" @click="load(page, cursors[page] ?? '')">Retry</button></p>
    <p v-else-if="!deployments.length">No deployments on this page.</p>
    <AppTable v-else fixed min-width="620px" table-class="deployments-table" wrap-class="deployments-table-wrap">
      <template #head><tr><th>Stack</th><th>Status</th><th>Started</th><th>Commit</th></tr></template>
      <tr
        v-for="deployment in deployments"
        :key="deployment.id"
        class="app-table-row--clickable deployment-row"
        tabindex="0"
        :aria-label="`Open deployment details for ${deployment.stack}`"
        @click="openDeployment(deployment.id)"
        @keydown="openDeploymentFromKeyboard($event, deployment.id)"
      >
        <td class="deployment-stack" :title="deployment.stack">{{ deployment.stack }}</td>
        <td>
          <AppStatusBadge
            class="status deployment-status-badge"
            :class="statusClass(deployment.status)"
            :variant="statusVariant(deployment.status)"
            filled
          >
            {{ statusLabel(deployment.status) }}<small v-if="deployment.attempt_count > 1">{{ deployment.attempt_count }} attempts</small>
          </AppStatusBadge>
        </td>
        <td class="deployment-started"><time :datetime="deployment.started_at">{{ formatDate(deployment.started_at) }}</time></td>
        <td>
          <a
            v-if="deployment.commit"
            class="deployment-commit-link"
            href="#"
            :aria-label="`Open Git commit ${deployment.commit}`"
            :title="deployment.commit"
            @click.prevent.stop="openCommit(deployment.commit)"
            @keydown.enter.stop
            @keydown.space.prevent.stop="openCommit(deployment.commit)"
          >{{ shortCommitHash(deployment.commit) }}</a>
          <span v-else>—</span>
        </td>
      </tr>
    </AppTable>
    <AppPagination class="deployment-pages" label="Deployment pages" :page="page" :loading="loading" :has-next="Boolean(nextCursor)" @previous="previous" @next="next" />
  </section>
</template>

<style scoped>
.deployments-filter-bar { display: flex; flex-wrap: wrap; column-gap: 12px; row-gap: 16px; margin: 8px 0 12px; }
.deployments-filter-field { display: flex; flex-direction: column; gap: 6px; width: min(100%, 260px); }
.deployments-filter-field > span { font-size: 0.8rem; color: var(--muted); }
:deep(.deployments-table th:nth-child(1)),
:deep(.deployments-table td:nth-child(1)) { width: 42%; }
:deep(.deployments-table th:nth-child(2)),
:deep(.deployments-table td:nth-child(2)) { width: 18%; }
:deep(.deployments-table th:nth-child(3)),
:deep(.deployments-table td:nth-child(3)) { width: 26%; }
:deep(.deployments-table th:nth-child(4)),
:deep(.deployments-table td:nth-child(4)) { width: 14%; }

.deployment-stack {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.deployment-started,
.deployment-status-badge,
.deployment-commit-link { white-space: nowrap; }

.deployment-status-badge {
  display: inline-flex;
  align-items: center;
  line-height: 1.2;
}
.deployment-status-badge small { margin-left: 6px; color: var(--muted); font-size: .72rem; font-weight: 500; }

.deployment-commit-link {
  display: inline-block;
  border-radius: 3px;
  color: var(--accent-strong);
  padding: 2px 3px;
  font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.8rem;
  text-decoration: none;
}

.deployment-commit-link:hover { text-decoration: underline; }
.deployment-commit-link:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
</style>
