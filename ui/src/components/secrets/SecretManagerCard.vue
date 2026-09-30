<script setup lang="ts">
import { computed } from "vue";

import type { SecretManagerInfo } from "../../api/types";

type ManagerStatus = "healthy" | "syncing" | "error" | "unavailable";

const props = withDefaults(defineProps<{
  manager: SecretManagerInfo;
  managedCount: number;
  syncing?: boolean;
  syncError?: string;
  feedback?: string;
}>(), {
  syncing: false,
  syncError: "",
  feedback: "",
});

const emit = defineEmits<{
  sync: [];
}>();

const title = computed(() => props.manager.provider?.name?.trim() || "cloud-secrets Secret Manager");

const providerLink = computed(() => {
  const raw = props.manager.provider?.link?.trim();
  if (!raw) {
    return "";
  }

  try {
    const parsed = new URL(raw);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.toString() : "";
  } catch {
    return "";
  }
});

const status = computed<ManagerStatus>(() => {
  if (props.syncing) {
    return "syncing";
  }
  if (props.syncError) {
    return "error";
  }
  if (!props.manager.available) {
    return "unavailable";
  }
  if (props.manager.error) {
    return "error";
  }

  return "healthy";
});

const statusLabel = computed(() => ({
  healthy: "Healthy",
  syncing: "Syncing…",
  error: "Error",
  unavailable: "Unavailable",
})[status.value]);

const statusMessage = computed(() => props.syncError.trim() || props.manager.error?.trim() || "");

const relativeSyncTime = computed(() => {
  if (!props.manager.last_sync_at) {
    return "Never synced";
  }

  const timestamp = new Date(props.manager.last_sync_at).valueOf();
  if (Number.isNaN(timestamp)) {
    return "Never synced";
  }

  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60000));
  if (elapsedMinutes < 1) {
    return "just now";
  }
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes} min ago`;
  }

  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) {
    return `${elapsedHours} ${elapsedHours === 1 ? "hour" : "hours"} ago`;
  }

  const elapsedDays = Math.floor(elapsedHours / 24);
  return `${elapsedDays} ${elapsedDays === 1 ? "day" : "days"} ago`;
});

const desktopSyncTime = computed(() => (
  relativeSyncTime.value === "Never synced" ? relativeSyncTime.value : `Last synced ${relativeSyncTime.value}`
));

const managedSecretsLabel = computed(() => (
  `${props.managedCount} managed ${props.managedCount === 1 ? "secret" : "secrets"}`
));

const syncDisabled = computed(() => (
  props.syncing || !props.manager.controllable || !props.manager.available
));
</script>

<template>
  <article class="secret-manager-card">
    <header class="secret-manager-card-header">
      <span class="secret-manager-card-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <path d="M7 18h10a4 4 0 0 0 .7-7.94A6 6 0 0 0 6.34 8.2 5 5 0 0 0 7 18Z" />
        </svg>
      </span>
      <div class="secret-manager-card-copy">
        <h3 class="secret-manager-card-title">
          <a v-if="providerLink" :href="providerLink" target="_blank" rel="noopener noreferrer">{{ title }}</a>
          <span v-else>{{ title }}</span>
        </h3>
        <p>Provides externally managed secrets</p>
      </div>
      <span class="secret-manager-badge" :class="`secret-manager-badge--${status}`" role="status">
        <span v-if="syncing" class="secret-manager-spinner" aria-hidden="true" />
        {{ statusLabel }}
      </span>
    </header>

    <footer class="secret-manager-card-footer">
      <div class="secret-manager-metadata">
        <span class="secret-manager-metadata-item">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <circle cx="8" cy="15" r="3" />
            <path d="m10.2 12.8 7.3-7.3 2 2-1.5 1.5 1 1-2 2-1-1-1.8 1.8" />
          </svg>
          {{ managedSecretsLabel }}
        </span>
        <span class="secret-manager-metadata-separator" aria-hidden="true">·</span>
        <span class="secret-manager-metadata-item secret-manager-sync-time">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
            <circle cx="12" cy="12" r="8" />
            <path d="M12 8v4l2.5 1.5" />
          </svg>
          <span class="secret-manager-sync-time-desktop">{{ desktopSyncTime }}</span>
          <span class="secret-manager-sync-time-mobile">{{ relativeSyncTime }}</span>
        </span>
      </div>

      <button
        type="button"
        class="secret-manager-sync"
        :disabled="syncDisabled"
        :aria-label="syncing ? 'Synchronizing secrets' : 'Synchronize secrets'"
        @click="emit('sync')"
      >
        <span v-if="syncing" class="secret-manager-spinner" aria-hidden="true" />
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <path d="M20 7v5h-5" />
          <path d="M4 17v-5h5" />
          <path d="M6.1 9a7 7 0 0 1 11.5-2L20 9M4 15l2.4 2a7 7 0 0 0 11.5-2" />
        </svg>
        {{ syncing ? "Syncing…" : "Sync" }}
      </button>
    </footer>

    <p v-if="statusMessage" class="secret-manager-message secret-manager-message--error">{{ statusMessage }}</p>
    <p v-else-if="feedback" class="secret-manager-message">{{ feedback }}</p>
  </article>
</template>
