<script setup lang="ts">
withDefaults(
  defineProps<{
    tag?: "article" | "section";
    headerClass?: string;
    contentClass?: string;
  }>(),
  {
    tag: "article",
    headerClass: "",
    contentClass: "",
  },
);
</script>

<template>
  <component :is="tag" class="app-panel">
    <header v-if="$slots.header" class="app-panel-header" :class="headerClass">
      <slot name="header" />
    </header>
    <div class="app-panel-content" :class="contentClass">
      <slot />
    </div>
  </component>
</template>
