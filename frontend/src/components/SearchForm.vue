<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import type { HealthStatus, SearchParams } from '@/api';
import type { FilterConfig } from '@/types';
import { Icons } from '@/components/ui';
import FilterIcon from '@/components/icons/FilterIcon.vue';
import { loadLocal, saveLocal } from '@/utils/storage';

const SEARCH_HISTORY_KEY = 'search_history';
const MAX_HISTORY_ITEMS = 8;

const props = defineProps<{
  backendHealth: HealthStatus | null;
  loading: boolean;
}>();

const emit = defineEmits<{
  (e: 'search', params: SearchParams): void;
  (e: 'searchComplete'): void;
}>();

const keyword = ref('');
const searchInput = ref<HTMLInputElement | null>(null);
const showAdvanced = ref(false);
const inputFocused = ref(false);
const includeKeywords = ref('');
const excludeKeywords = ref('');

const loadHistory = (): string[] => {
  try {
    const stored = loadLocal(SEARCH_HISTORY_KEY);
    if (!stored) return [];
    const parsed = JSON.parse(stored);
    return Array.isArray(parsed)
      ? parsed.filter((item): item is string => typeof item === 'string').slice(0, MAX_HISTORY_ITEMS)
      : [];
  } catch {
    return [];
  }
};

const searchHistory = ref<string[]>(loadHistory());
const hasActiveFilter = computed(() => Boolean(includeKeywords.value.trim() || excludeKeywords.value.trim()));
const showHistory = computed(() => inputFocused.value && !showAdvanced.value && searchHistory.value.length > 0);

const loadUserConfig = () => {
  try {
    const savedPlugins = loadLocal('plugins');
    const savedDiskTypes = loadLocal('disk_types');
    if (savedPlugins !== null) {
      return {
        plugins: savedPlugins ? JSON.parse(savedPlugins) : [],
        cloudTypes: savedDiskTypes ? JSON.parse(savedDiskTypes) : undefined
      };
    }
    return {
      plugins: props.backendHealth?.plugins || [],
      cloudTypes: savedDiskTypes ? JSON.parse(savedDiskTypes) : undefined
    };
  } catch (error) {
    console.error('加载配置失败:', error);
    return { plugins: [], cloudTypes: undefined };
  }
};

const rememberKeyword = (value: string) => {
  const next = [value, ...searchHistory.value.filter(item => item !== value)].slice(0, MAX_HISTORY_ITEMS);
  searchHistory.value = next;
  saveLocal(SEARCH_HISTORY_KEY, JSON.stringify(next));
};

const clearHistory = () => {
  searchHistory.value = [];
  saveLocal(SEARCH_HISTORY_KEY, '[]');
};

const handleSearch = (historyKeyword?: string) => {
  if (historyKeyword) keyword.value = historyKeyword;
  const normalizedKeyword = keyword.value.trim();
  if (!normalizedKeyword || props.loading) return;

  inputFocused.value = false;
  rememberKeyword(normalizedKeyword);

  const userConfig = loadUserConfig();
  const params: SearchParams = { kw: normalizedKeyword, res: 'merge', src: 'plugin' };
  if (userConfig.plugins?.length) params.plugins = userConfig.plugins.join(',');
  if (userConfig.cloudTypes?.length) {
    (params as SearchParams & { cloud_types?: string }).cloud_types = userConfig.cloudTypes.join(',');
  }

  const filterConfig: FilterConfig = {};
  if (includeKeywords.value.trim()) {
    filterConfig.include = includeKeywords.value.split(/[,\s]+/).filter(Boolean);
  }
  if (excludeKeywords.value.trim()) {
    filterConfig.exclude = excludeKeywords.value.split(/[,\s]+/).filter(Boolean);
  }
  if (filterConfig.include || filterConfig.exclude) {
    (params as SearchParams & { filter?: string }).filter = JSON.stringify(filterConfig);
  }

  emit('search', params);
};

const handleBlur = () => {
  window.setTimeout(() => {
    inputFocused.value = false;
  }, 120);
};

const handleGlobalShortcut = (event: KeyboardEvent) => {
  const target = event.target as HTMLElement | null;
  const isTyping = target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || target?.isContentEditable;
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault();
    searchInput.value?.focus();
    searchInput.value?.select();
  } else if (event.key === '/' && !isTyping) {
    event.preventDefault();
    searchInput.value?.focus();
  } else if (event.key === 'Escape') {
    showAdvanced.value = false;
    inputFocused.value = false;
    searchInput.value?.blur();
  }
};

onMounted(() => window.addEventListener('keydown', handleGlobalShortcut));
onUnmounted(() => window.removeEventListener('keydown', handleGlobalShortcut));
</script>

<template>
  <div class="search-container">
    <div class="search-field">
      <button
        type="button"
        class="filter-button"
        :class="{ active: showAdvanced, 'has-filter': hasActiveFilter }"
        :title="hasActiveFilter ? '已启用筛选条件' : '高级筛选'"
        aria-label="高级筛选"
        @click="showAdvanced = !showAdvanced"
      >
        <FilterIcon :size="16" />
      </button>

      <input
        ref="searchInput"
        v-model="keyword"
        type="search"
        class="search-input"
        placeholder="搜索电影、剧集、软件或学习资料"
        autocomplete="off"
        :disabled="props.loading"
        @focus="inputFocused = true"
        @blur="handleBlur"
        @keydown.enter="handleSearch()"
      />

      <button
        type="button"
        class="search-button"
        :disabled="props.loading || !keyword.trim()"
        @click="handleSearch()"
      >
        <component :is="props.loading ? Icons.Loading() : Icons.Search()" :class="['search-icon', props.loading && 'spinning']" />
        <span>{{ props.loading ? '搜索中' : '搜索' }}</span>
      </button>

      <div v-if="showHistory" class="history-popover">
        <div class="history-header">
          <span>最近搜索</span>
          <button type="button" @mousedown.prevent="clearHistory">清空</button>
        </div>
        <div class="history-list">
          <button
            v-for="item in searchHistory"
            :key="item"
            type="button"
            @mousedown.prevent="handleSearch(item)"
          >
            <component :is="Icons.Clock()" />
            <span>{{ item }}</span>
          </button>
        </div>
      </div>
    </div>

    <Transition name="filter-panel">
      <div v-if="showAdvanced" class="advanced-panel">
        <label>
          <span>必须包含</span>
          <input v-model="includeKeywords" type="text" placeholder="例如：4K HDR 国语" @keydown.enter="handleSearch()" />
        </label>
        <label>
          <span>排除内容</span>
          <input v-model="excludeKeywords" type="text" placeholder="例如：预告 花絮 CAM" @keydown.enter="handleSearch()" />
        </label>
        <p>多个关键词用空格或逗号分隔</p>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.search-container { position: relative; width: 100%; }
.search-field { position: relative; width: 100%; }

.search-input {
  width: 100%; height: 2.75rem; padding: 0 6rem 0 2.75rem;
  border: 1px solid hsl(var(--border)); border-radius: 0.55rem; outline: none;
  background: hsl(var(--card)); color: hsl(var(--foreground)); font-size: 0.875rem;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}
.search-input:hover { border-color: hsl(var(--border-hover)); }
.search-input:focus { border-color: hsl(var(--primary)); box-shadow: 0 0 0 3px hsl(var(--primary) / 0.1); }
.search-input::-webkit-search-cancel-button { margin-right: 0.25rem; }

.filter-button, .search-button {
  position: absolute; top: 0.3rem; height: 2.15rem; display: inline-flex;
  align-items: center; justify-content: center; border: 0; border-radius: 0.4rem; cursor: pointer;
}
.filter-button { left: 0.3rem; width: 2.15rem; background: transparent; color: hsl(var(--muted-foreground)); }
.filter-button:hover, .filter-button.active { color: hsl(var(--foreground)); background: hsl(var(--muted)); }
.filter-button.has-filter::after {
  content: ''; position: absolute; top: 0.22rem; right: 0.22rem; width: 0.35rem; height: 0.35rem;
  border-radius: 50%; background: hsl(var(--primary));
}
.search-button {
  right: 0.3rem; gap: 0.35rem; min-width: 5.15rem; padding: 0 0.75rem;
  color: hsl(var(--primary-foreground)); background: hsl(var(--primary)); font-size: 0.8125rem; font-weight: 600;
}
.search-button:hover:not(:disabled) { background: hsl(var(--primary) / 0.9); }
.search-button:disabled { cursor: not-allowed; opacity: 0.45; }
.search-icon { width: 0.95rem; height: 0.95rem; }
.spinning { animation: spin 0.8s linear infinite; }

.history-popover {
  position: absolute; top: calc(100% + 0.35rem); left: 0; right: 0; z-index: 40;
  padding: 0.4rem; border: 1px solid hsl(var(--border)); border-radius: 0.55rem; background: hsl(var(--card));
}
.history-header { display: flex; align-items: center; justify-content: space-between; padding: 0.25rem 0.45rem 0.4rem; color: hsl(var(--muted-foreground)); font-size: 0.75rem; }
.history-header button { border: 0; background: transparent; color: inherit; cursor: pointer; font-size: inherit; }
.history-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.15rem; }
.history-list button {
  min-width: 0; height: 2rem; display: flex; align-items: center; gap: 0.45rem; padding: 0 0.5rem;
  border: 0; border-radius: 0.35rem; background: transparent; color: hsl(var(--foreground)); cursor: pointer;
  font-size: 0.8125rem; text-align: left;
}
.history-list button:hover { background: hsl(var(--muted)); }
.history-list svg { width: 0.85rem; height: 0.85rem; flex: 0 0 auto; color: hsl(var(--muted-foreground)); }
.history-list span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.advanced-panel {
  display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.55rem;
  margin-top: 0.45rem; padding: 0.65rem; border: 1px solid hsl(var(--border));
  border-radius: 0.55rem; background: hsl(var(--card));
}
.advanced-panel label { display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 0.5rem; }
.advanced-panel label > span { color: hsl(var(--muted-foreground)); font-size: 0.75rem; white-space: nowrap; }
.advanced-panel input {
  min-width: 0; height: 2rem; padding: 0 0.55rem; border: 1px solid hsl(var(--border));
  border-radius: 0.35rem; outline: none; background: hsl(var(--background)); color: hsl(var(--foreground)); font-size: 0.8125rem;
}
.advanced-panel input:focus { border-color: hsl(var(--primary)); }
.advanced-panel p { grid-column: 1 / -1; margin: 0; color: hsl(var(--muted-foreground)); font-size: 0.6875rem; }

.filter-panel-enter-active, .filter-panel-leave-active { transition: opacity 0.15s ease, transform 0.15s ease; }
.filter-panel-enter-from, .filter-panel-leave-to { opacity: 0; transform: translateY(-4px); }
@keyframes spin { to { transform: rotate(360deg); } }

@media (max-width: 560px) {
  .search-input { height: 2.5rem; padding-right: 3rem; }
  .filter-button, .search-button { top: 0.25rem; height: 2rem; }
  .filter-button { left: 0.25rem; width: 2rem; }
  .search-button { right: 0.25rem; min-width: 2.25rem; width: 2.25rem; padding: 0; }
  .search-button span { display: none; }
  .history-list, .advanced-panel { grid-template-columns: 1fr; }
  .advanced-panel p { grid-column: auto; }
}
</style>
