<script setup lang="ts">
defineProps<{
  name: "calendar" | "check" | "clock" | "commit" | "config" | "failed" | "network" | "running" | "secret" | "service" | "stack" | "volume" | "warning";
}>();
</script>

<template>
  <svg class="deployment-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <template v-if="name === 'stack'">
      <path d="m12 3-9 5 9 5 9-5-9-5Z" /><path d="m3 12 9 5 9-5" /><path d="m3 16 9 5 9-5" />
    </template>
    <template v-else-if="name === 'commit'">
      <circle cx="6" cy="5" r="2" /><circle cx="18" cy="7" r="2" /><circle cx="10" cy="19" r="2" />
      <path d="M6 7v5a4 4 0 0 0 4 4v1M8 5h4a6 6 0 0 1 6 6v-2" />
    </template>
    <template v-else-if="name === 'calendar'">
      <rect x="3" y="5" width="18" height="16" rx="2" /><path d="M16 3v4M8 3v4M3 10h18M8 14h.01M12 14h.01M16 14h.01M8 18h.01M12 18h.01" />
    </template>
    <template v-else-if="name === 'clock'">
      <circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" />
    </template>
    <template v-else-if="name === 'config'">
      <path d="M6 3h8l4 4v14H6V3Z" /><path d="M14 3v5h5M9 13h6M9 17h6" />
    </template>
    <template v-else-if="name === 'service'">
      <path d="m12 3 8 4.5v9L12 21l-8-4.5v-9L12 3Z" /><path d="m4.5 7.8 7.5 4.3 7.5-4.3M12 12v9" />
    </template>
    <template v-else-if="name === 'network'">
      <circle cx="12" cy="4" r="2" /><circle cx="5" cy="19" r="2" /><circle cx="19" cy="19" r="2" /><path d="m11 6-5 11M13 6l5 11M7 19h10" />
    </template>
    <template v-else-if="name === 'secret'">
      <circle cx="8" cy="15" r="4" /><path d="m11 12 8-8 2 2-2 2 1 1-2 2-1-1-3 3M5 18l-2 2" />
    </template>
    <template v-else-if="name === 'volume'">
      <ellipse cx="12" cy="5" rx="8" ry="3" /><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" />
    </template>
    <template v-else-if="name === 'check'">
      <path d="m7 12 3 3 7-7" />
    </template>
    <template v-else-if="name === 'failed'">
      <path d="m8 8 8 8M16 8l-8 8" />
    </template>
    <template v-else-if="name === 'running'">
      <path d="M12 3a9 9 0 1 0 9 9" /><path d="M12 7v5l3 2" />
    </template>
    <template v-else>
      <path d="M12 8v5M12 17h.01" /><path d="M10.3 3.7 2.6 17a2 2 0 0 0 1.7 3h15.4a2 2 0 0 0 1.7-3L13.7 3.7a2 2 0 0 0-3.4 0Z" />
    </template>
  </svg>
</template>

<style scoped>
.deployment-icon { width: 1.25em; height: 1.25em; display: block; flex: 0 0 auto; }
</style>
