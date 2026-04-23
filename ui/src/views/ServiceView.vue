<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchServiceDeployments, fetchServiceStatus } from "../api/overview";
import { fetchServices } from "../api/services";
import type {
  ServiceDeploymentResponse,
  ServiceInfo,
  ServiceStatusResponse,
} from "../api/types";
import { useSecretDetailsStore } from "../stores/secretDetails";
import { formatBytes, formatDate, formatNanoCPU } from "../utils/format";

const route = useRoute();

const loading = ref(false);
const loadingError = ref("");
const serviceInfo = ref<ServiceInfo | null>(null);
const serviceStatus = ref<ServiceStatusResponse | null>(null);
const serviceDeployments = ref<ServiceDeploymentResponse[]>([]);
const deploymentsLoading = ref(false);
const deploymentsError = ref("");
const showDockerLabels = ref(false);
const secretDetailsStore = useSecretDetailsStore();

const stackName = computed(() => String(route.params.stack ?? "").trim());
const serviceName = computed(() => String(route.params.service ?? "").trim());

const serviceTitle = computed(() => `${stackName.value}/${serviceName.value}`);
const serviceSpec = computed(() => serviceStatus.value?.spec ?? null);
function sortedLabelEntries(labels: Record<string, string> | undefined): Array<[string, string]> {
  if (!labels || typeof labels !== "object") {
    return [];
  }

  return Object.entries(labels).sort(([left], [right]) => left.localeCompare(right));
}
const customServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.custom));
const dockerServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.docker));
const serviceSecrets = computed(() => {
  const secrets = serviceSpec.value?.secrets;
  return Array.isArray(secrets) ? secrets : [];
});
const serviceRoutes = computed(() => {
  const routes = serviceInfo.value?.web_routes;
  return Array.isArray(routes) ? routes : [];
});
const serviceNetworkNames = computed(() => {
  const networks = serviceSpec.value?.network;
  if (!Array.isArray(networks)) {
    return [];
  }

  const names = networks
    .map((network) => {
      const target = String(network.target ?? "").trim();
      if (!target) {
        return "";
      }

      // Network target can be a raw Docker ID; hide it because UI should only show names.
      if (/^[a-f0-9]{24,}$/i.test(target)) {
        return "";
      }

      return target;
    })
    .filter((name): name is string => name.length > 0);

  return Array.from(new Set(names)).sort((left, right) => left.localeCompare(right));
});
const deployments = computed(() => {
  const items = serviceDeployments.value;
  return Array.isArray(items) ? items : [];
});

function deploymentStatusClass(status: ServiceDeploymentResponse["status"]): string {
  if (status === "success") {
    return "success";
  }
  if (status === "failed") {
    return "failed";
  }
  return "unknown";
}

function deploymentKey(item: ServiceDeploymentResponse, index: number): string {
  return `${item.created_at}-${item.status}-${item.image_version}-${index}`;
}

function openSecretDetails(secretName: string): void {
  void secretDetailsStore.openSecretDetails(secretName);
}

function toggleDockerLabels(): void {
  showDockerLabels.value = !showDockerLabels.value;
}

async function loadServiceDetails() {
  if (!stackName.value || !serviceName.value) {
    loadingError.value = "Invalid service route parameters";
    serviceInfo.value = null;
    serviceStatus.value = null;
    return;
  }

  loading.value = true;
  loadingError.value = "";

  try {
    const [servicesResult, statusResult] = await Promise.allSettled([
      fetchServices(),
      fetchServiceStatus(stackName.value, serviceName.value),
    ]);
    const errors: string[] = [];

    if (servicesResult.status === "fulfilled") {
      const services = Array.isArray(servicesResult.value.services) ? servicesResult.value.services : [];
      serviceInfo.value =
        services.find((item) => item.stack === stackName.value && item.name === serviceName.value) ?? null;
      if (!serviceInfo.value) {
        errors.push(`Service metadata not found for ${stackName.value}/${serviceName.value}`);
      }
    } else {
      serviceInfo.value = null;
      errors.push(servicesResult.reason instanceof Error ? servicesResult.reason.message : "Failed to load services");
    }

    if (statusResult.status === "fulfilled") {
      serviceStatus.value = statusResult.value;
    } else {
      serviceStatus.value = null;
      errors.push(
        statusResult.reason instanceof Error ? statusResult.reason.message : "Failed to load service status",
      );
    }

    if (errors.length > 0) {
      loadingError.value = errors.join("; ");
    }
  } catch (error) {
    serviceInfo.value = null;
    serviceStatus.value = null;
    serviceDeployments.value = [];
    loadingError.value = error instanceof Error ? error.message : "Failed to load service details";
  } finally {
    loading.value = false;
  }
}

async function loadServiceDeployments() {
  if (!stackName.value || !serviceName.value) {
    serviceDeployments.value = [];
    deploymentsError.value = "Invalid service route parameters";
    return;
  }

  deploymentsLoading.value = true;
  deploymentsError.value = "";
  try {
    const response = await fetchServiceDeployments(stackName.value, serviceName.value);
    serviceDeployments.value = Array.isArray(response.deployments) ? response.deployments : [];
  } catch (error) {
    serviceDeployments.value = [];
    deploymentsError.value = error instanceof Error ? error.message : "Failed to load service deployments";
  } finally {
    deploymentsLoading.value = false;
  }
}

watch(
  [stackName, serviceName],
  () => {
    showDockerLabels.value = false;
    void loadServiceDetails();
    void loadServiceDeployments();
  },
  { immediate: true },
);
</script>

<template>
  <section class="services-page">
    <header class="services-header">
      <h2>{{ serviceTitle }}</h2>
    </header>

    <div v-if="loading && !serviceStatus" class="services-empty">
      <p class="meta">Loading service details...</p>
    </div>

    <div v-else-if="!serviceStatus" class="services-empty">
      <p class="meta">Failed to load service details: {{ loadingError || "unknown error" }}</p>
    </div>

    <div v-else class="service-details-layout">
      <article class="stack-card service-details-card">
        <h3 class="stack-title">Service</h3>
        <table class="service-status-summary-table" aria-label="Service details">
          <tbody>
            <tr>
              <th scope="row">Name</th>
              <td>{{ serviceInfo?.name || serviceName }}</td>
            </tr>
            <tr>
              <th scope="row">Stack</th>
              <td>{{ serviceInfo?.stack || stackName }}</td>
            </tr>
            <tr>
              <th scope="row">Type</th>
              <td>{{ serviceInfo?.type_title || serviceInfo?.type || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Description</th>
              <td>{{ serviceInfo?.description || "n/a" }}</td>
            </tr>
            <tr>
              <th scope="row">Image</th>
              <td>{{ serviceInfo?.image || serviceSpec?.image || "n/a" }}</td>
            </tr>
            <tr v-if="serviceInfo && serviceInfo.repository_url">
              <th scope="row">Repository</th>
              <td>
                <a
                  :href="serviceInfo.repository_url"
                  class="assistant-md-link"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {{ serviceInfo.repository_url }}
                </a>
              </td>
            </tr>
            <tr>
              <th scope="row">Web Routes</th>
              <td>
                <ul v-if="serviceRoutes.length > 0" class="service-details-tags">
                  <li v-for="routeItem in serviceRoutes" :key="`${routeItem.domain}-${routeItem.address}-${routeItem.port}`">
                    {{ routeItem.domain }} ({{ routeItem.address }}:{{ routeItem.port }})
                  </li>
                </ul>
                <span v-else>n/a</span>
              </td>
            </tr>
            <tr>
              <th scope="row">Labels</th>
              <td>
                <ul v-if="customServiceLabels.length > 0" class="event-details">
                  <li v-for="[key, value] in customServiceLabels" :key="key" class="event-detail">
                    <span class="event-detail-key">{{ key }}</span>
                    <code class="event-detail-value">{{ value }}</code>
                  </li>
                </ul>
                <span v-else class="meta">No labels.</span>
              </td>
            </tr>
            <tr v-if="dockerServiceLabels.length > 0">
              <th scope="row">Docker Labels</th>
              <td>
                <ul class="event-details">
                  <li class="event-detail">
                    <button
                      type="button"
                      class="service-secret-badge status unknown"
                      :aria-expanded="showDockerLabels ? 'true' : 'false'"
                      @click="toggleDockerLabels"
                    >
                      {{ showDockerLabels ? "Hide" : "Show" }}
                    </button>
                  </li>
                </ul>
                <ul
                  v-if="showDockerLabels"
                  class="event-details service-details-docker-tags"
                >
                  <li v-for="[key, value] in dockerServiceLabels" :key="key" class="event-detail">
                    <span class="event-detail-key">{{ key }}</span>
                    <code class="event-detail-value">{{ value }}</code>
                  </li>
                </ul>
              </td>
            </tr>
            <tr>
              <th scope="row">Networks</th>
              <td>
                <ul v-if="serviceNetworkNames.length > 0" class="service-details-tags">
                  <li v-for="networkName in serviceNetworkNames" :key="networkName">
                    {{ networkName }}
                  </li>
                </ul>
                <span v-else>n/a</span>
              </td>
            </tr>
            <tr>
              <th scope="row">Secrets</th>
              <td>
                <ul v-if="serviceSecrets.length > 0" class="service-details-tags service-secrets-list">
                  <li v-for="secret in serviceSecrets" :key="`${secret.secret_name}-${secret.secret_id}`">
                    <button
                      type="button"
                      class="service-secret-badge status unknown"
                      :disabled="!secret.secret_name"
                      @click="secret.secret_name && openSecretDetails(secret.secret_name)"
                    >
                      {{ secret.secret_name || "n/a" }}
                    </button>
                  </li>
                </ul>
                <span v-else>n/a</span>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="loadingError" class="meta">Warning: {{ loadingError }}</p>
      </article>

      <div class="service-details-side">
        <article class="stack-card service-resources-card">
          <h3 class="stack-title">Resources</h3>
          <table class="service-status-summary-table" aria-label="Service resources">
            <tbody>
              <tr>
                <th scope="row">Deploy mode</th>
                <td>{{ serviceSpec?.mode || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">Requested / limited RAM</th>
                <td>{{ serviceSpec?.requested_ram_bytes ? formatBytes(serviceSpec?.requested_ram_bytes) : 0 }} / {{ serviceSpec?.limit_ram_bytes ? formatBytes(serviceSpec?.limit_ram_bytes) : '∞' }}</td>
              </tr>
              <tr>
                <th scope="row">Requested / limited CPU</th>
                <td>{{ serviceSpec?.requested_cpu_nano ? formatNanoCPU(serviceSpec?.requested_cpu_nano) : 0 }} / {{ serviceSpec?.limit_cpu_nano? formatNanoCPU(serviceSpec?.limit_cpu_nano) : '∞' }}</td>
              </tr>
            </tbody>
          </table>
        </article>

        <article class="stack-card service-deployments-card">
          <h3 class="stack-title">Latest deployments</h3>
          <p v-if="deploymentsLoading" class="meta">Loading deployments...</p>
          <p v-else-if="deploymentsError" class="meta">Failed to load deployments: {{ deploymentsError }}</p>
          <p v-else-if="deployments.length === 0" class="meta">No deployments yet.</p>
          <ul v-else class="service-deployments-list">
            <li v-for="(deployment, index) in deployments" :key="deploymentKey(deployment, index)" class="service-deployment-item">
              <div class="service-deployment-head">
                <span class="status" :class="deploymentStatusClass(deployment.status)">
                  {{ deployment.status }}
                </span>
                <span class="meta">{{ formatDate(deployment.created_at) }}</span>
              </div>
              <p class="meta">image version: {{ deployment.image_version || "n/a" }}</p>
              <p v-if="deployment.commit" class="meta">commit: {{ deployment.commit }}</p>
            </li>
          </ul>
        </article>
      </div>
    </div>
  </section>
</template>
