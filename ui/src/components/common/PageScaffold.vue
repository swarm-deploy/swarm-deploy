<script setup lang="ts">
import { t } from "../../i18n";
defineProps<{
  title: string;
  description: string;
}>();
</script>

<template>
  <section class="status-panel">
    <p class="meta">{{ description }}</p>
  </section>
  <section>
    <article class="stack-card">
      <h2>{{ title }}</h2>
      <p class="meta">{{ t('common.scaffold') }}</p>
    </article>
  </section>
</template>
