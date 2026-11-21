<script setup lang="ts" generic="T, Q">
import { onBeforeUnmount, ref, shallowRef, watch } from "vue";
import AppButton from "./AppButton.vue";

defineOptions({ inheritAttrs: false });
const props = defineProps<{
  label: string;
  query: Q;
  loadPage: (query: Q, cursor?: string) => Promise<{ items: T[]; nextCursor?: string }>;
  hideWhenEmpty?: boolean;
}>();
defineSlots<{
  default(props: { items: T[]; loading: boolean; error: string; retry: () => void }): unknown;
}>();

const items = shallowRef<T[]>([]);
const page = ref(0);
const loading = ref(false);
const error = ref("");
const nextCursor = ref<string>();
let cursors: (string | undefined)[] = [undefined];
let target: { index: number; cursor?: string } = { index: 0 };
let requestId = 0;

async function load(index: number, cursor?: string) {
  const current = ++requestId;
  target = { index, cursor };
  loading.value = true;
  error.value = "";
  try {
    const result = await props.loadPage(props.query, cursor);
    if (requestId !== current) return;
    items.value = result.items;
    nextCursor.value = result.nextCursor;
    page.value = index;
    cursors[index] = cursor;
    cursors.length = index + 1;
  } catch (reason) {
    if (requestId === current) error.value = reason instanceof Error && reason.message ? reason.message : "Request failed";
  } finally {
    if (requestId === current) loading.value = false;
  }
}

function retry() {
  if (!loading.value) void load(target.index, target.cursor);
}

watch(() => props.query, () => {
  items.value = [];
  nextCursor.value = undefined;
  page.value = 0;
  cursors = [undefined];
  void load(0);
}, { immediate: true });
onBeforeUnmount(() => { requestId++; });
</script>

<template>
  <slot :items="items" :loading="loading" :error="error" :retry="retry" />
  <nav
    v-if="!hideWhenEmpty || items.length > 0 || page > 0"
    v-bind="$attrs"
    class="app-pagination"
    :aria-label="label"
  >
    <AppButton variant="secondary" compact :disabled="loading || page === 0" @click="load(page - 1, cursors[page - 1])">Previous</AppButton>
    <span>Page {{ page + 1 }}</span>
    <AppButton variant="secondary" compact :disabled="loading || !nextCursor" @click="load(page + 1, nextCursor)">Next</AppButton>
  </nav>
</template>

<style scoped>
.app-pagination {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
}

</style>
