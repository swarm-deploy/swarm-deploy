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
    <template v-else-if="name === 'realtime'">
      <path d="M3 12h4l2.5-6 4.5 12 2.5-6H21" />
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
    <template v-else-if="name === 'alerts'">
      <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9Z" />
      <path d="M10 21h4" />
    </template>
    <template v-else-if="name === 'assistant'">
      <path d="M5 5.5h14a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-8l-5 3v-3H5a2 2 0 0 1-2-2v-8a2 2 0 0 1 2-2Z" />
      <path d="M8 10.5h8m-8 3h5" />
    </template>
    <template v-else-if="name === 'more'">
      <circle cx="5" cy="12" r="1.25" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.25" fill="currentColor" stroke="none" />
      <circle cx="19" cy="12" r="1.25" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="name === 'search'">
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m15.5 15.5 5 5" />
    </template>
    <template v-else-if="name === 'close'">
      <path d="m6 6 12 12M18 6 6 18" />
    </template>
    <template v-else-if="name === 'chevron'">
      <path d="m9 5 7 7-7 7" />
    </template>
    <template v-else-if="name === 'appearance'">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
    </template>
    <template v-else-if="name === 'user'">
      <circle cx="12" cy="8" r="4" />
      <path d="M4.5 21a7.5 7.5 0 0 1 15 0" />
    </template>
    <template v-else-if="name === 'github'">
      <path d="M12 2a10 10 0 0 0-3.16 19.49c.5.09.68-.22.68-.48v-1.88c-2.78.6-3.37-1.18-3.37-1.18-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.9 1.53 2.35 1.09 2.92.83.09-.65.35-1.09.64-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.64 0 0 .84-.27 2.75 1.02A9.58 9.58 0 0 1 12 6.52c.85 0 1.69.11 2.48.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.37.2 2.39.1 2.64.64.7 1.03 1.59 1.03 2.68 0 3.84-2.34 4.68-4.57 4.93.36.31.68.92.68 1.85v3.08c0 .27.18.58.69.48A10 10 0 0 0 12 2Z" fill="currentColor" stroke="none" />
    </template>
    <template v-else-if="name === 'install'">
      <path d="M12 3v12m-5-5 5 5 5-5M4 15v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4" />
    </template>
    <template v-else-if="name === 'panel-close'">
      <rect x="3.5" y="4" width="17" height="16" rx="2" />
      <path d="M9 4v16m6.5-11.5L12 12l3.5 3.5" />
    </template>
    <template v-else-if="name === 'panel-open'">
      <rect x="3.5" y="4" width="17" height="16" rx="2" />
      <path d="M9 4v16m3-11.5 3.5 3.5-3.5 3.5" />
    </template>
  </svg>
</template>
