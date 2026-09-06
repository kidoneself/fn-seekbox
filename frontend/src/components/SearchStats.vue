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

// 计算网盘类型数量
const diskTypeCount = computed(() => {
  return Object.keys(props.mergedResults || {}).length;
});
</script>

<template>
  <div class="card">
    <div class="stats-layout">
      <div class="stats-metrics">
        <!-- 搜索结果总数 -->
        <div class="stats-chip stats-chip-primary">
          <component :is="Icons.Grid()" class="w-4 h-4 text-muted-foreground" />
          <span class="stats-label stats-label-desktop text-sm text-muted-foreground">搜索结果</span>
          <span class="stats-label stats-label-mobile text-sm text-muted-foreground">结果</span>
          <span class="font-semibold text-foreground">{{ total.toLocaleString() }}</span>
        </div>
        
        <!-- 网盘类型数量 -->
        <div class="stats-chip">
          <component :is="Icons.Folder()" class="w-4 h-4 text-muted-foreground" />
          <span class="stats-label stats-label-desktop text-sm text-muted-foreground">网盘类型</span>
          <span class="stats-label stats-label-mobile text-sm text-muted-foreground">类型</span>
          <span class="font-semibold text-foreground">{{ diskTypeCount }}</span>
        </div>
        
        <!-- 搜索耗时 -->
        <div v-if="searchTime" class="stats-chip stats-chip-time">
          <component :is="Icons.Clock()" class="w-4 h-4 text-muted-foreground" />
          <span class="stats-label stats-label-desktop text-sm text-muted-foreground">用时</span>
          <span class="stats-label stats-label-mobile text-sm text-muted-foreground">用时</span>
          <span class="font-semibold text-foreground">{{ searchTime }}ms</span>
        </div>
      </div>

      <div class="stats-actions">
        <button
          v-if="canExport"
          :disabled="loading"
          @click="$emit('export-results')"
          title="批量导出"
          aria-label="批量导出"
          class="action-button action-button-label"
        >
          <component :is="Icons.Download()" class="w-4 h-4 action-icon"/>
          <span class="action-label">批量导出</span>
        </button>

        <!-- 强制刷新按钮 -->
        <button
          :disabled="loading"
          @click="$emit('force-refresh')"
          title="强制刷新"
          aria-label="强制刷新"
          class="action-button"
        >
          <component :is="Icons.Refresh()" class="w-5 h-5 action-icon"/>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stats-layout {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.75rem 0.9rem;
}

.card {
  border-radius: 0.85rem;
  background: hsl(var(--card) / 0.9);
  box-shadow: 0 6px 22px hsl(var(--shadow-color) / 0.04);
}

.stats-metrics {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.75rem 1.5rem;
  min-width: 0;
}

.stats-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
  white-space: nowrap;
  padding: 0.3rem 0.5rem;
  border-radius: 0.5rem;
  background: hsl(var(--muted) / 0.55);
}

.stats-label-mobile {
  display: none;
}

.stats-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.625rem;
  flex-shrink: 0;
}

.action-button {
  appearance: none;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  transition: all 0.2s ease;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 2.25rem;
  border-radius: 9999px;
}

.action-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.action-button:focus-visible {
  outline: 2px solid #93c5fd;
  outline-offset: 2px;
}

.action-button:hover:not(:disabled) {
  border-color: hsl(var(--border-hover));
  background: hsl(var(--muted) / 0.55);
}

.action-button-label {
  gap: 0.5rem;
  padding: 0 0.9rem;
  font-size: 0.875rem;
  font-weight: 600;
}

.action-label {
  white-space: nowrap;
}

.action-icon {
  flex-shrink: 0;
}

.stats-actions .action-button:last-child {
  width: 2.25rem;
  height: 2.25rem;
  padding: 0;
}

@media (max-width: 768px) {
  .stats-layout {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    padding: 0.625rem 0.75rem;
    gap: 0.75rem;
  }

  .stats-metrics {
    flex: 1 1 auto;
    min-width: 0;
    flex-wrap: nowrap;
    justify-content: flex-start;
    gap: 0.95rem;
    padding-right: 0.4rem;
  }

  .stats-chip {
    flex: 0 0 auto;
    min-width: 0;
    gap: 0.28rem;
    overflow: hidden;
    font-size: 0.75rem;
  }

  .stats-chip .font-semibold {
    min-width: 0;
    font-size: 0.75rem;
    line-height: 1;
  }

  .stats-chip :deep(svg),
  .stats-chip > :global(svg) {
    width: 0.9rem;
    height: 0.9rem;
  }

  .stats-label-desktop {
    display: none;
  }

  .stats-label-mobile {
    display: inline;
    font-size: 0.75rem;
    line-height: 1;
  }

  .stats-chip-primary {
    flex: 0 0 auto;
    min-width: auto;
  }

  .stats-actions {
    width: auto;
    justify-content: flex-end;
    gap: 0.45rem;
    flex-shrink: 0;
    margin-left: auto;
  }

  .action-button,
  .stats-actions .action-button:last-child {
    width: 2rem;
    height: 2rem;
    min-height: 2rem;
    padding: 0;
  }

  .action-button-label {
    flex: 0 0 auto;
  }

  .action-label {
    display: none;
  }

  .action-icon {
    width: 0.95rem;
    height: 0.95rem;
  }
}

.stats-layout {
  padding: 0.4rem 0.55rem;
}

.card {
  border-radius: 0.5rem;
  box-shadow: none;
}

.stats-chip {
  padding: 0 0.15rem;
  border-radius: 0;
  background: transparent;
}

.action-button {
  min-height: 2rem;
  border-radius: 0.4rem;
}

.stats-actions .action-button:last-child {
  width: 2rem;
  height: 2rem;
}
</style>
