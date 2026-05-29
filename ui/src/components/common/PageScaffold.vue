<script setup lang="ts">
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
      <p class="meta">Scaffold page. Functional implementation is planned for next iteration.</p>
    </article>
  </section>
</template>
