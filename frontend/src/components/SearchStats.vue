<script setup lang="ts">
import { computed } from 'vue';
import type { MergedResults } from '@/types';
import { Icons } from '@/components/ui';

const props = defineProps<{
  total: number;
  mergedResults: MergedResults;
  loading: boolean;
  searchTime?: number;
  isUpdating: boolean;
  updateCount: number;
  canExport: boolean;
}>();

defineEmits<{
  (e: 'exportResults'): void;
  (e: 'forceRefresh'): void;
}>();

const diskTypeCount = computed(() => Object.keys(props.mergedResults || {}).length);
</script>

<template>
  <div class="search-summary">
    <div class="summary-copy" aria-live="polite">
      <strong>{{ total.toLocaleString() }}</strong>
      <span>个结果</span>
      <span class="summary-separator">·</span>
      <span>{{ diskTypeCount }} 种类型</span>
      <template v-if="searchTime">
        <span class="summary-separator">·</span>
        <span>{{ searchTime }}ms</span>
      </template>
      <template v-if="isUpdating">
        <span class="summary-separator">·</span>
        <span class="updating">仍在补充结果</span>
      </template>
    </div>

    <div class="summary-actions">
      <button
        v-if="canExport"
        type="button"
        :disabled="loading"
        title="批量导出搜索结果"
        @click="$emit('exportResults')"
      >
        <component :is="Icons.Download()" />
        <span>导出</span>
      </button>
      <button
        type="button"
        :disabled="loading"
        title="重新搜索并绕过缓存"
        @click="$emit('forceRefresh')"
      >
        <component :is="Icons.Refresh()" />
        <span>刷新</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.search-summary {
  min-height: 2.35rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.25rem 0.15rem;
  color: hsl(var(--muted-foreground));
  font-size: 0.75rem;
}

.summary-copy {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 0.35rem;
  white-space: nowrap;
}

.summary-copy strong {
  color: hsl(var(--foreground));
  font-size: 0.875rem;
}

.summary-separator {
  color: hsl(var(--border-hover));
}

.updating {
  color: hsl(var(--primary));
}

.summary-actions {
  display: flex;
  align-items: center;
  gap: 0.3rem;
  flex: 0 0 auto;
}

.summary-actions button {
  height: 1.9rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.3rem;
  padding: 0 0.5rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.35rem;
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  cursor: pointer;
  font-size: 0.75rem;
}

.summary-actions button:hover:not(:disabled) {
  background: hsl(var(--muted));
}

.summary-actions button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.summary-actions svg {
  width: 0.85rem;
  height: 0.85rem;
}

@media (max-width: 520px) {
  .summary-copy > span:nth-of-type(3),
  .summary-copy > span:nth-of-type(4) {
    display: none;
  }

  .summary-actions button {
    width: 1.9rem;
    padding: 0;
  }

  .summary-actions button span {
    display: none;
  }
}
</style>
