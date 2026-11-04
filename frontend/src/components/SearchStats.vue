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
  (e: 'reset'): void;
}>();

const diskTypeCount = computed(() => Object.values(props.mergedResults || {}).filter(items => items.length > 0).length);
</script>

<template>
  <div class="search-summary">
    <div class="summary-copy" aria-live="polite">
      <span class="summary-label">搜索结果</span>
      <strong>{{ total.toLocaleString() }}</strong><span>条</span>
      <span class="summary-types">{{ diskTypeCount }} 种类型</span>
      <span v-if="searchTime" class="summary-time">耗时 {{ searchTime >= 1000 ? (searchTime / 1000).toFixed(2) + ' 秒' : searchTime + 'ms' }}</span>
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
      <button
        type="button"
        title="清空本次搜索，回到初始状态"
        aria-label="重置搜索"
        @click="$emit('reset')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M3 10a9 9 0 1 1 2.6 8.4M3 4v6h6" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <span>重置</span>
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
  .summary-label,.summary-time { display: none; }

  .summary-actions button {
    width: 1.9rem;
    padding: 0;
  }

  .summary-actions button span {
    display: none;
  }
}

.search-summary { min-height: 44px; font-size: 13px; color: #475569; }
.summary-copy { gap: 7px; }.summary-copy strong { font-size: 24px; line-height: 1.2; font-variant-numeric: tabular-nums; }.summary-label { font-weight: 600; color: hsl(var(--foreground)); margin-right: 3px; }
.summary-types { margin-left: 7px; padding-left: 14px; border-left: 1px solid hsl(var(--border)); color: hsl(var(--foreground)); }.summary-time { margin-left: 8px; font-size: 12px; }
@media(max-width:520px) { .summary-copy { gap: 5px; }.summary-copy strong { font-size: 22px; }.summary-types { margin-left: 4px; padding-left: 9px; font-size: 12px; } }
</style>
