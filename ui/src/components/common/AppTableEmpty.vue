<script setup lang="ts">
import { t } from "../../i18n";
withDefaults(
  defineProps<{
    message?: string;
  }>(),
  {
    message: t("common.noData"),
  },
);
</script>

<template>
  <div class="app-table-empty">
    <p class="meta"><slot>{{ message }}</slot></p>
  </div>
</template>
