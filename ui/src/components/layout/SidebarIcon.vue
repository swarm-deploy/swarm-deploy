<script setup lang="ts">
defineProps<{
  name: string;
}>();
</script>

<template>
  <svg class="sidebar-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <template v-if="name === 'overview'">
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
    </template>
    <template v-else-if="name === 'services'">
      <path d="m12 3 8 4.5-8 4.5-8-4.5L12 3Z" />
      <path d="m4 12 8 4.5 8-4.5M4 16.5l8 4.5 8-4.5" />
    </template>
    <template v-else-if="name === 'graph'">
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="18" cy="8" r="2.5" />
      <circle cx="9" cy="18" r="2.5" />
      <path d="m8.3 6.5 7.3 1M7 8.3l1.3 7.2m3-1.2 4.8-4.1" />
    </template>
    <template v-else-if="name === 'cluster'">
      <rect x="3.5" y="4" width="17" height="5" rx="1.5" />
      <rect x="3.5" y="15" width="17" height="5" rx="1.5" />
      <path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6" />
    </template>
    <template v-else-if="name === 'networks'">
      <circle cx="12" cy="5" r="2.5" />
      <circle cx="5" cy="18" r="2.5" />
      <circle cx="19" cy="18" r="2.5" />
      <path d="m10.8 7.2-4.6 8.6m7-8.6 4.6 8.6M7.5 18h9" />
    </template>
    <template v-else-if="name === 'secrets'">
      <circle cx="8" cy="12" r="4.5" />
      <path d="M12.5 12H21m-3 0v3m-3-3v2" />
    </template>
    <template v-else-if="name === 'recommendations'">
      <path d="M9 18h6m-5 3h4" />
      <path d="M8.2 15.2a7 7 0 1 1 7.6 0c-.6.4-.8.9-.8 1.8H9c0-.9-.2-1.4-.8-1.8Z" />
    </template>
    <template v-else-if="name === 'events'">
      <path d="M6 3.5v3m12-3v3M4 9.5h16" />
      <rect x="3.5" y="5.5" width="17" height="15" rx="2" />
      <path d="m8 15 2.2 2 5-5" />
    </template>
    <template v-else-if="name === 'user'">
      <circle cx="12" cy="8" r="4" />
      <path d="M4.5 21a7.5 7.5 0 0 1 15 0" />
    </template>
  </svg>
</template>
