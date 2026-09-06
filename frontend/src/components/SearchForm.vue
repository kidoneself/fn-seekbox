<script setup lang="ts">
import { ref, computed } from 'vue';
import type { SearchParams } from '@/api';
import type { HealthStatus } from '@/api';
import type { FilterConfig } from '@/types';
import { Button, Input, Icons } from '@/components/ui';
import FilterIcon from '@/components/icons/FilterIcon.vue';
import { loadLocal } from '@/utils/storage';

const keyword = ref('');
const loading = ref(false);
const showAdvanced = ref(false);
const includeKeywords = ref('');
const excludeKeywords = ref('');
const hasActiveFilter = computed(() => Boolean(includeKeywords.value.trim() || excludeKeywords.value.trim()));

// 接收后端健康状态作为 props
const props = defineProps<{
  backendHealth: HealthStatus | null;
}>();

const emit = defineEmits<{
  (e: 'search', params: SearchParams): void;
  (e: 'searchComplete'): void;
}>();

// 从配置中加载用户设置和后端默认配置
const loadUserConfig = () => {
  try {
    const savedPlugins = loadLocal('plugins');
    const savedDiskTypes = loadLocal('disk_types');
    
    // 如果用户已手动设置，使用用户设置
    if (savedPlugins !== null) {
      return {
        channels: [],
        plugins: savedPlugins ? JSON.parse(savedPlugins) : [],
        cloudTypes: savedDiskTypes ? JSON.parse(savedDiskTypes) : undefined
      };
    }
    
    // 如果用户未设置，使用传入的后端配置
    if (props.backendHealth) {
      return {
        channels: [],
        plugins: props.backendHealth.plugins || [],
        cloudTypes: savedDiskTypes ? JSON.parse(savedDiskTypes) : undefined
      };
    }
    
    return {
      channels: [],
      plugins: [],
      cloudTypes: undefined
    };
  } catch (err) {
    console.error('加载配置失败:', err);
    return {
      channels: [],
      plugins: [],
      cloudTypes: undefined
    };
  }
};

const handleSearch = () => {
  if (!keyword.value.trim()) {
    return;
  }
  
  loading.value = true;
  
  // 加载配置（用户设置或后端默认缓存）
  const userConfig = loadUserConfig();
  
  // 判断有哪些数据源
  const hasPlugins = userConfig.plugins && userConfig.plugins.length > 0;
  
  const params: SearchParams = {
    kw: keyword.value,
    res: 'merge',
    src: 'plugin'
  };
  
  if (hasPlugins) {
    params.plugins = userConfig.plugins.join(',');
  }
  
  if (userConfig.cloudTypes && userConfig.cloudTypes.length > 0) {
    // 将网盘类型数组转为逗号分隔的字符串
    (params as any).cloud_types = userConfig.cloudTypes.join(',');
  }
  
  // 添加过滤配置（使用后端filter参数格式）
  const filterConfig: FilterConfig = {};
  if (includeKeywords.value.trim()) {
    filterConfig.include = includeKeywords.value.split(/[,\s]+/).filter(k => k.trim());
  }
  if (excludeKeywords.value.trim()) {
    filterConfig.exclude = excludeKeywords.value.split(/[,\s]+/).filter(k => k.trim());
  }
  if (filterConfig.include || filterConfig.exclude) {
    (params as any).filter = JSON.stringify(filterConfig);
  }
  
  emit('search', params);
  
  // 2秒后重置loading状态
  setTimeout(() => {
    loading.value = false;
    // 不再触发searchComplete事件
  }, 2000);
};
</script>

<template>
  <div class="w-full mx-auto">
    <div class="search-container">
      <!-- 主搜索框 -->
      <div class="relative w-full">
        <div class="search-bar">
          <div class="search-field">
            <!-- 筛选按钮（最左侧） -->
            <button
              type="button"
              @click="showAdvanced = !showAdvanced"
              :class="[
                'advanced-toggle-left',
                showAdvanced && 'active',
                hasActiveFilter && 'has-filter'
              ]"
              :title="hasActiveFilter ? '已启用筛选条件' : '高级筛选'"
              aria-label="高级筛选"
            >
              <FilterIcon :size="16" />
            </button>
            <input
              v-model="keyword"
              type="text"
              placeholder="搜索电影、剧集、软件或学习资料"
              :disabled="loading"
              @keydown.enter="handleSearch"
              class="search-input"
            />
            <!-- 搜索按钮 -->
            <button
              type="button"
              @click="handleSearch"
              :disabled="loading || !keyword.trim()"
              class="search-submit"
            >
              <component 
                :is="loading ? Icons.Loading() : Icons.Send()" 
                :class="[
                  'w-4 h-4',
                  loading && 'animate-spin'
                ]"
              />
              <span>{{ loading ? '搜索中' : '搜索' }}</span>
            </button>
          </div>
        </div>
      </div>
      
      <!-- 高级选项面板 -->
      <Transition name="slide-down">
        <div v-if="showAdvanced" class="advanced-panel">
          <div class="advanced-content">
            <!-- 包含关键词 -->
            <div class="filter-input-group">
              <label class="filter-label">
                <span class="label-text">包含关键词</span>
                <span class="label-hint">结果中至少包含一个关键词 (OR关系)，多个关键词用空格或英文逗号(,)分隔</span>
              </label>
              <input
                v-model="includeKeywords"
                type="text"
                placeholder="高码 hdr 4k"
                class="filter-input"
                @keydown.enter="handleSearch"
              />
              <div v-if="includeKeywords.trim()" class="filter-preview">
                <span class="preview-label">包含:</span>
                <span 
                  v-for="(word, index) in includeKeywords.split(/[,\s]+/).filter(w => w.trim())" 
                  :key="index"
                  class="filter-tag include"
                >
                  {{ word }}
                </span>
              </div>
            </div>
            
            <!-- 排除关键词 -->
            <div class="filter-input-group">
              <label class="filter-label">
                <span class="label-text">排除关键词</span>
                <span class="label-hint">结果中包含任意一个关键词就排除 (OR关系)，多个关键词用空格或英文逗号(,)分隔</span>
              </label>
              <input
                v-model="excludeKeywords"
                type="text"
                placeholder="预告 花絮 枪版 CAM TS"
                class="filter-input"
                @keydown.enter="handleSearch"
              />
              <div v-if="excludeKeywords.trim()" class="filter-preview">
                <span class="preview-label">排除:</span>
                <span 
                  v-for="(word, index) in excludeKeywords.split(/[,\s]+/).filter(w => w.trim())" 
                  :key="index"
                  class="filter-tag exclude"
                >
                  {{ word }}
                </span>
              </div>
            </div>
          </div>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style scoped>
.search-container {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  width: 100%;
}

.search-bar,
.search-field {
  position: relative;
  width: 100%;
}

.search-input {
  width: 100%;
  height: 3.45rem;
  padding: 0 7rem 0 3.4rem;
  border: 0;
  border-radius: 0.8rem;
  outline: 0;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  font-size: 1rem;
  font-weight: 520;
  box-shadow: inset 0 0 0 1px hsl(var(--border));
  transition: box-shadow 0.2s ease, background 0.2s ease;
}

.search-input::placeholder {
  color: hsl(var(--muted-foreground) / 0.82);
}

.search-input:hover {
  box-shadow: inset 0 0 0 1px hsl(var(--border-hover));
}

.search-input:focus {
  background: hsl(var(--card));
  box-shadow: inset 0 0 0 1.5px hsl(var(--primary)), 0 0 0 4px hsl(var(--primary) / 0.1);
}

.search-input:disabled {
  opacity: 0.7;
}

.search-submit {
  position: absolute;
  top: 0.42rem;
  right: 0.42rem;
  height: calc(100% - 0.84rem);
  min-width: 5.4rem;
  padding: 0 1rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.45rem;
  border: 0;
  border-radius: 0.62rem;
  color: white;
  background: linear-gradient(135deg, hsl(var(--primary)), hsl(var(--accent-secondary)));
  box-shadow: 0 6px 16px hsl(var(--primary) / 0.25);
  font-size: 0.875rem;
  font-weight: 700;
  cursor: pointer;
  transition: transform 0.18s ease, box-shadow 0.18s ease, opacity 0.18s ease;
}

.search-submit:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 8px 20px hsl(var(--primary) / 0.32);
}

.search-submit:disabled {
  cursor: not-allowed;
  opacity: 0.45;
  box-shadow: none;
}

/* 高级选项切换按钮（左侧） */
.advanced-toggle-left {
  position: absolute;
  left: 0;
  top: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 2.45rem;
  height: 2.45rem;
  top: 0.5rem;
  left: 0.5rem;
  border-radius: 0.62rem;
  background: transparent;
  border: none;
  border: 0;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
  transition: all 0.2s ease;
  z-index: 10;
}

.advanced-toggle-left:hover {
  background: hsl(var(--accent));
  color: hsl(var(--accent-foreground));
}

.advanced-toggle-left.active {
  background: hsl(var(--primary) / 0.12);
  color: hsl(var(--primary));
}

.advanced-toggle-left.has-filter {
  background: hsl(var(--primary) / 0.1);
  color: hsl(var(--primary));
}

.advanced-toggle-left.has-filter::after {
  content: '';
  position: absolute;
  top: 4px;
  right: 4px;
  width: 8px;
  height: 8px;
  background: hsl(var(--primary));
  border-radius: 50%;
  border: 2px solid hsl(var(--background));
}

/* 高级选项面板 */
.advanced-panel {
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: 0.9rem;
  padding: 1rem;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
}

@media (max-width: 520px) {
  .search-input {
    height: 3.2rem;
    padding-right: 3.3rem;
    padding-left: 3.15rem;
    font-size: 0.9rem;
  }

  .search-submit {
    min-width: 2.45rem;
    width: 2.45rem;
    padding: 0;
  }

  .search-submit span {
    display: none;
  }

  .advanced-toggle-left {
    top: 0.38rem;
    left: 0.38rem;
  }

  .label-hint {
    display: none;
  }
}

.advanced-content {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

/* 过滤模式选择 */
.filter-mode {
  display: flex;
  gap: 0.75rem;
}

.mode-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  background: hsl(var(--background));
  border: 1px solid hsl(var(--border));
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  flex: 1;
}

.mode-label:has(.mode-radio:checked) {
  background: hsl(var(--primary) / 0.08);
  border-color: hsl(var(--primary));
}

.mode-label:hover {
  border-color: hsl(var(--primary) / 0.5);
}

.mode-radio {
  width: 16px;
  height: 16px;
  cursor: pointer;
  accent-color: hsl(var(--primary));
  margin: 0;
}

.mode-text {
  font-weight: 500;
  color: hsl(var(--foreground));
  font-size: 0.875rem;
}

/* 关键词输入 */
.filter-input-group {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.filter-label {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
}

.label-text {
  font-weight: 500;
  color: hsl(var(--foreground));
  font-size: 0.875rem;
}

.label-hint {
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
}

.filter-input {
  width: 100%;
  padding: 0.5rem 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 6px;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  font-size: 0.875rem;
  transition: all 0.2s ease;
}

.filter-input:focus {
  outline: none;
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 3px hsl(var(--primary) / 0.1);
}

.filter-input::placeholder {
  color: hsl(var(--muted-foreground));
}

/* 过滤预览 */
.filter-preview {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
  padding: 0.5rem;
  background: hsl(var(--muted) / 0.3);
  border-radius: 6px;
}

.preview-label {
  font-size: 0.8125rem;
  font-weight: 500;
  color: hsl(var(--muted-foreground));
}

.filter-tag {
  display: inline-flex;
  align-items: center;
  padding: 0.25rem 0.75rem;
  border-radius: 6px;
  font-size: 0.8125rem;
  font-weight: 500;
  transition: all 0.2s ease;
}

.filter-tag.include {
  background: hsl(142, 76%, 36% / 0.1);
  color: hsl(142, 76%, 36%);
  border: 1px solid hsl(142, 76%, 36% / 0.3);
}

.filter-tag.exclude {
  background: hsl(0, 84%, 60% / 0.1);
  color: hsl(0, 84%, 60%);
  border: 1px solid hsl(0, 84%, 60% / 0.3);
}

/* 过渡动画 */
.slide-down-enter-active,
.slide-down-leave-active {
  transition: all 0.3s ease;
}

.slide-down-enter-from {
  opacity: 0;
  transform: translateY(-10px);
}

.slide-down-leave-to {
  opacity: 0;
  transform: translateY(-10px);
}

/* 响应式 */
@media (max-width: 768px) {
  .advanced-panel {
    padding: 0.75rem;
  }
  
  .filter-mode {
    flex-direction: column;
  }
}

.search-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  width: 100%;
  margin-bottom: 2rem;
}

.search-input-wrapper {
  position: relative;
  width: 100%;
  flex: 1;
}

.search-input {
  width: 100%;
  padding: 0.75rem 1rem;
  border-radius: 0.75rem;
  border: 1px solid hsl(var(--border));
  background-color: hsl(var(--card));
  color: hsl(var(--foreground));
  font-size: 1rem;
  outline: none;
  transition: all 0.2s ease;
}

.search-input::placeholder {
  color: hsl(var(--muted-foreground));
}

.search-input:focus {
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 3px hsl(var(--primary) / 0.2);
}

.search-button {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0.75rem 1.5rem;
  border-radius: 0.75rem;
  background-color: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  font-weight: 500;
  font-size: 1rem;
  border: none;
  cursor: pointer;
  transition: all 0.2s ease;
}

.search-button:hover:not(:disabled) {
  background-color: hsl(var(--primary) / 0.88);
}

.search-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.loading-spinner {
  display: inline-block;
  width: 1rem;
  height: 1rem;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-radius: 50%;
  border-top-color: #fff;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (min-width: 768px) {
  .search-form {
    flex-direction: row;
    align-items: center;
  }
  
  .search-button {
    width: auto;
    white-space: nowrap;
  }
}

/* 紧凑工具栏 */
.search-input {
  height: 2.5rem;
  padding: 0 5.25rem 0 2.85rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.5rem;
  font-size: 0.875rem;
  font-weight: 400;
  box-shadow: none;
}

.search-input:hover {
  border-color: hsl(var(--border-hover));
  box-shadow: none;
}

.search-input:focus {
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 2px hsl(var(--primary) / 0.08);
}

.search-submit {
  top: 0.25rem;
  right: 0.25rem;
  height: 2rem;
  min-width: 4.4rem;
  padding: 0 0.75rem;
  border-radius: 0.35rem;
  background: hsl(var(--primary));
  box-shadow: none;
  font-size: 0.8125rem;
  font-weight: 600;
}

.search-submit:hover:not(:disabled) {
  transform: none;
  background: hsl(var(--primary) / 0.9);
  box-shadow: none;
}

.advanced-toggle-left {
  width: 2.25rem;
  height: 2.25rem;
  top: 0.125rem;
  left: 0.125rem;
  border-radius: 0.35rem;
}

.advanced-panel {
  border-radius: 0.5rem;
  padding: 0.75rem;
  box-shadow: none;
}

@media (max-width: 520px) {
  .search-input {
    height: 2.5rem;
  }

  .advanced-toggle-left {
    top: 0.125rem;
    left: 0.125rem;
  }
}
</style>
