<script setup lang="ts">
withDefaults(
  defineProps<{
    tag?: "article" | "div" | "section";
  }>(),
  {
    tag: "article",
  },
);
</script>

<template>
  <component :is="tag" class="app-card">
    <slot name="header" />
    <slot />
    <slot name="footer" />
  </component>
</template>
