<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(
  defineProps<{
    ariaLabel?: string;
    fixed?: boolean;
    minWidth?: string;
    summary?: boolean;
    tableClass?: string;
    wrapClass?: string;
  }>(),
  {
    ariaLabel: undefined,
    fixed: false,
    minWidth: undefined,
    summary: false,
    tableClass: "",
    wrapClass: "",
  },
);

const tableStyle = computed(() => (props.minWidth ? { minWidth: props.minWidth } : undefined));
</script>

<template>
  <div class="app-table-wrap" :class="wrapClass">
    <table
      class="app-table"
      :class="[{ 'app-table--fixed': fixed, 'app-table--summary': summary }, tableClass]"
      :style="tableStyle"
      :aria-label="ariaLabel"
    >
      <slot name="colgroup" />
      <thead v-if="$slots.head">
        <slot name="head" />
      </thead>
      <tbody>
        <slot />
      </tbody>
    </table>
  </div>
</template>
