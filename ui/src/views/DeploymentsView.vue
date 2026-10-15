<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { fetchDeploymentFilters, fetchDeployments } from "../api/overview";
import type { DeploymentFilterOptions, DeploymentSummary } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppPagination from "../components/common/AppPagination.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const store = useOverviewStore();
const route = useRoute();
const router = useRouter();
const selectedStack = ref(typeof route.query.stack_name === "string" ? route.query.stack_name.trim() : "");
const selectedService = ref(typeof route.query.service_name === "string" ? route.query.service_name.trim() : "");
const stackOptions = ref<string[]>([]);
const serviceOptions = ref<DeploymentFilterOptions["services"]>([]);
const stackSearch = ref("");
const serviceSearch = ref("");
const stackFilter = ref<HTMLDetailsElement | null>(null);
const serviceFilter = ref<HTMLDetailsElement | null>(null);
const filteredStacks = computed(() => {
  const options = [...stackOptions.value];
  if (selectedStack.value && !options.includes(selectedStack.value)) options.push(selectedStack.value);
  const search = stackSearch.value.trim().toLocaleLowerCase();
  return options.sort((left, right) => left.localeCompare(right))
    .filter((stack) => !search || stack.toLocaleLowerCase().includes(search));
});
const filteredServices = computed(() => {
  const options = serviceOptions.value.filter((service) => !selectedStack.value || service.stack_name === selectedStack.value);
  if (selectedService.value && !options.some((service) => service.service_name === selectedService.value)) {
    options.push({ stack_name: selectedStack.value, service_name: selectedService.value });
  }
  const search = serviceSearch.value.trim().toLocaleLowerCase();
  return options.sort((left, right) => left.stack_name.localeCompare(right.stack_name) || left.service_name.localeCompare(right.service_name))
    .filter((service) => !search || `${service.stack_name} ${service.service_name}`.toLocaleLowerCase().includes(search));
});
const selectedServiceLabel = computed(() => {
  if (!selectedService.value) return "All services";
  if (selectedStack.value) return selectedService.value;
  const matches = serviceOptions.value.filter((service) => service.service_name === selectedService.value);
  return matches.length === 1 ? `${selectedService.value} — ${matches[0].stack_name}` : selectedService.value;
});
const deployments = ref<DeploymentSummary[]>([]);
const error = ref("");
const loading = ref(false);
const nextCursor = ref("");
const cursors = ref<string[]>([""]);
const page = ref(0);
let request = 0;

function startedAtTimestamp(deployment: DeploymentSummary): number {
  const timestamp = Date.parse(deployment.started_at);
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

async function load(index: number, cursor: string) {
  const current = ++request;
  loading.value = true;
  error.value = "";
  try {
    const result = await fetchDeployments({ stack: selectedStack.value || undefined, serviceName: selectedService.value || undefined, limit: 20, cursor: cursor || undefined });
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

function selectStack(stack: string): void {
  if (stack !== selectedStack.value && selectedService.value && !serviceOptions.value.some(
    (service) => service.stack_name === stack && service.service_name === selectedService.value,
  )) selectedService.value = "";
  selectedStack.value = stack;
  stackSearch.value = "";
  if (stackFilter.value) stackFilter.value.open = false;
  persistFilters();
}

function selectService(service?: DeploymentFilterOptions["services"][number]): void {
  selectedService.value = service?.service_name ?? "";
  if (service && !selectedStack.value) selectedStack.value = service.stack_name;
  serviceSearch.value = "";
  if (serviceFilter.value) serviceFilter.value.open = false;
  persistFilters();
}

function persistFilters(): void {
  const query = { ...route.query };
  if (selectedStack.value) query.stack_name = selectedStack.value;
  else delete query.stack_name;
  if (selectedService.value) query.service_name = selectedService.value;
  else delete query.service_name;
  void router.replace({ query });
}

watch(() => [route.query.stack_name, route.query.service_name], ([stackValue, serviceValue]) => {
  const stack = typeof stackValue === "string" ? stackValue.trim() : "";
  const service = typeof serviceValue === "string" ? serviceValue.trim() : "";
  if (stack !== selectedStack.value) selectedStack.value = stack;
  if (service !== selectedService.value) selectedService.value = service;
});
watch([selectedStack, selectedService], () => {
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
    <p class="meta">Apply attempts and their desired changes. Service convergence is not verified.</p>
    <div class="deployments-filter-bar"><div class="deployments-filter-field">
      <span>Stack</span>
      <details ref="stackFilter" class="deployments-stack-filter">
        <summary aria-label="Filter deployments by stack">{{ selectedStack || "All stacks" }} <span aria-hidden="true">⌄</span></summary>
        <div class="deployments-stack-menu">
          <input v-model="stackSearch" type="search" placeholder="Search stacks" aria-label="Search stacks" />
          <button type="button" :class="{ selected: !selectedStack }" @click="selectStack('')">All stacks</button>
          <button v-for="stack in filteredStacks" :key="stack" type="button" :class="{ selected: selectedStack === stack }" @click="selectStack(stack)">{{ stack }}</button>
          <p v-if="filteredStacks.length === 0">No matching stacks</p>
        </div>
      </details>
    </div>
    <div class="deployments-filter-field">
      <span>Service</span>
      <details ref="serviceFilter" class="deployments-stack-filter">
        <summary aria-label="Filter deployments by service">{{ selectedServiceLabel }} <span aria-hidden="true">⌄</span></summary>
        <div class="deployments-stack-menu">
          <input v-model="serviceSearch" type="search" placeholder="Search services" aria-label="Search services" />
          <button type="button" :class="{ selected: !selectedService }" @click="selectService()">All services</button>
          <button v-for="service in filteredServices" :key="`${service.stack_name}/${service.service_name}`" type="button" :class="{ selected: selectedService === service.service_name }" @click="selectService(service)">{{ service.service_name }}<small v-if="!selectedStack && service.stack_name">{{ service.stack_name }}</small></button>
          <p v-if="filteredServices.length === 0">No matching services</p>
        </div>
      </details>
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
          <span class="status deployment-status-badge" :class="statusClass(deployment.status)">
            {{ statusLabel(deployment.status) }}
          </span>
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
.deployments-filter-bar { display: flex; flex-wrap: wrap; gap: 12px; }
.deployments-filter-field { display: flex; flex-direction: column; gap: 6px; width: min(100%, 260px); margin: 18px 0; }
.deployments-filter-field > span { font-size: 0.8rem; color: var(--muted); }
.deployments-stack-filter { position: relative; }
.deployments-stack-filter > summary { display: flex; justify-content: space-between; gap: 12px; padding: 8px 10px; border: 1px solid var(--border-default); border-radius: 6px; background: var(--surface-elevated); cursor: pointer; list-style: none; }
.deployments-stack-filter > summary::-webkit-details-marker { display: none; }
.deployments-stack-menu { box-sizing: border-box; position: absolute; z-index: 30; width: max(100%, 260px); max-width: calc(100vw - 2rem); max-height: 260px; overflow: auto; padding: 6px; background: var(--surface-elevated); border: 1px solid var(--border-default); border-radius: 6px; box-shadow: var(--shadow-popover); }
.deployments-stack-menu input { box-sizing: border-box; width: 100%; margin-bottom: 4px; padding: 7px; }
.deployments-stack-menu button { display: block; width: 100%; padding: 8px; border: 0; background: transparent; color: inherit; text-align: left; cursor: pointer; }
.deployments-stack-menu small { display: block; color: var(--muted); }
.deployments-stack-menu button:hover, .deployments-stack-menu button.selected { background: var(--surface-muted); }
.deployments-stack-menu p { margin: 8px; color: var(--muted); }
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
  background: var(--surface-muted);
  line-height: 1.2;
}

.deployment-status-badge.success { background: var(--success-muted); }
.deployment-status-badge.failed { background: var(--error-muted); }
.deployment-status-badge--running {
  color: var(--info);
  border-color: var(--accent-border);
  background: var(--info-muted);
}

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
