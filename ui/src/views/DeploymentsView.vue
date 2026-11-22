<script setup lang="ts">
import { deploymentStatus } from "../utils/deployments";
import { plural, t } from "../i18n";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { fetchDeploymentFilters, fetchDeployments } from "../api/overview";
import type { DeploymentFilterOptions, DeploymentSummary } from "../api/types";
import AppButton from "../components/common/AppButton.vue";
import AppSelect from "../components/common/AppSelect.vue";
import AppPagination from "../components/common/AppPagination.vue";
import AppStatusBadge from "../components/common/AppStatusBadge.vue";
import AppTable from "../components/common/AppTable.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
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
const stackSelectionLabel = computed(() => selectionLabel(selectedStacks.value, t("deployments.allStacks"), "common.stacksSelected"));
const serviceSelectionLabel = computed(() => selectionLabel(selectedServices.value, t("deployments.allServices"), "common.servicesSelected"));

function normalizeQueryValues(value: unknown): string[] {
  const values = Array.isArray(value) ? value : [value];
  return [...new Set(values.filter((item): item is string => typeof item === "string")
    .map((item) => item.trim()).filter(Boolean))];
}

function selectionLabel(values: string[], placeholder: string, countKey: string): string {
  if (values.length === 0) return placeholder;
  if (values.length <= 2) return values.join(", ");
  return plural(countKey, values.length);
}

function sameValues(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function startedAtTimestamp(deployment: DeploymentSummary): number {
  const timestamp = Date.parse(deployment.started_at);
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

const pageQuery = computed(() => ({
  stack: selectedStacks.value,
  serviceName: selectedServices.value,
  limit: 20,
}));

async function loadDeployments(filters: typeof pageQuery.value, cursor?: string) {
  const result = await fetchDeployments({ ...filters, cursor });
  const items = Array.isArray(result.deployments)
    ? [...result.deployments].sort((left, right) => startedAtTimestamp(right) - startedAtTimestamp(left))
    : [];
  return { items, nextCursor: result.next_cursor };
}

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
  void fetchDeploymentFilters().then((options) => {
    stackOptions.value = Array.isArray(options.stacks) ? options.stacks : [];
    serviceOptions.value = Array.isArray(options.services) ? options.services : [];
  }).catch(() => { stackOptions.value = []; serviceOptions.value = []; });
});
</script>

<template>
  <section class="services-page deployments-page">
    <header class="services-header"><h2>{{ t('deployments.title') }}</h2></header>
    <div class="deployments-filter-bar" :aria-label="t('deployments.filters')">
      <div class="deployments-filter-field">
        <AppSelect
          multiple
          :model-value="selectedStacks"
          :options="stackSelectOptions"
          :label="t('deployments.filterStack')"
          :placeholder="t('deployments.allStacks')"
          :heading="t('common.stacks')"
          :selection-label="stackSelectionLabel"
          searchable
          :search-label="t('common.searchStacks')"
          :search-placeholder="t('common.searchStacks')"
          :empty-message="t('common.noMatchingStacks')"
          @update:model-value="selectStacks"
        >
          <template #icon><SidebarIcon class="deployments-filter-icon" name="services" /></template>
        </AppSelect>
      </div>
      <div class="deployments-filter-field">
        <AppSelect
          multiple
          :model-value="selectedServices"
          :options="serviceSelectOptions"
          :label="t('deployments.filterService')"
          :placeholder="t('deployments.allServices')"
          :heading="t('common.services')"
          :selection-label="serviceSelectionLabel"
          searchable
          :search-label="t('common.searchServices')"
          :search-placeholder="t('common.searchServices')"
          :empty-message="t('common.noMatchingServices')"
          @update:model-value="selectServices"
        >
          <template #icon><DeploymentIcon class="deployments-filter-icon" name="service" /></template>
        </AppSelect>
      </div>
    </div>
    <AppPagination
      class="deployment-pages"
      :label="t('deployments.pages')"
      :query="pageQuery"
      :load-page="loadDeployments"
      v-slot="{ items: deployments, loading, error, retry }"
    >
      <p v-if="loading" role="status">{{ t('deployments.loading') }}</p>
      <p v-else-if="error" role="alert">{{ error }} <AppButton compact @click="retry">{{ t('common.retry') }}</AppButton></p>
      <p v-else-if="!deployments.length">{{ t('deployments.emptyPage') }}</p>
      <AppTable v-else fixed min-width="620px" table-class="deployments-table" wrap-class="deployments-table-wrap">
        <template #head><tr><th>{{ t('common.stack') }}</th><th>{{ t('common.status') }}</th><th>{{ t('common.started') }}</th><th>{{ t('common.commit') }}</th></tr></template>
        <tr
          v-for="deployment in deployments"
          :key="deployment.id"
          class="app-table-row--clickable deployment-row"
          tabindex="0"
          :aria-label="t('deployments.openDetails', { stack: deployment.stack })"
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
              {{ deploymentStatus(deployment.status) }}<small v-if="deployment.attempt_count > 1">{{ plural('deployments.attemptCount', deployment.attempt_count) }}</small>
            </AppStatusBadge>
          </td>
          <td class="deployment-started"><time :datetime="deployment.started_at">{{ formatDate(deployment.started_at) }}</time></td>
          <td>
            <a
              v-if="deployment.commit"
              class="deployment-commit-link"
              href="#"
              :aria-label="t('common.openGitCommit', { commit: deployment.commit })"
              :title="deployment.commit"
              @click.prevent.stop="openCommit(deployment.commit)"
              @keydown.enter.stop
              @keydown.space.prevent.stop="openCommit(deployment.commit)"
            >{{ shortCommitHash(deployment.commit) }}</a>
            <span v-else>—</span>
          </td>
        </tr>
      </AppTable>
    </AppPagination>
  </section>
</template>

<style scoped>
.deployments-page { gap: 16px; min-width: 0; }
.deployments-filter-bar {
  min-width: 0;
  display: grid;
  grid-template-columns: repeat(2, minmax(180px, 260px));
  gap: 10px;
  align-items: center;
  padding: 12px;
  border: 1px solid var(--border-subtle);
  border-radius: 10px;
  background: var(--surface-card-soft);
}
.deployments-filter-field { min-width: 0; color: var(--muted); }
.deployments-filter-icon {
  width: 17px;
  height: 17px;
  flex: 0 0 17px;
  color: var(--muted);
  fill: none;
  stroke: currentColor;
  stroke-width: 1.6;
  pointer-events: none;
}
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

@media (max-width: 700px) {
  .deployments-page { gap: 12px; }
  .deployments-filter-bar { grid-template-columns: repeat(2, minmax(0, 1fr)); padding: 10px; }
}

@media (max-width: 460px) {
  .deployments-filter-bar { grid-template-columns: minmax(0, 1fr); }
}
</style>
