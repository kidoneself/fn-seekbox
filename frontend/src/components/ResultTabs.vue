<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue';
import QRCode from 'qrcode';
import { inspectVisibleLinks } from '@/api';
import type {
  DetectionSettings,
  LinkCheckItem,
  LinkHealthRecord,
  LinkHealthState,
  MergedResults,
  MergedResultItem,
} from '@/types';
import { getDiskTypeName } from '@/utils/diskTypes';
import {
  buildHealthCacheKey,
  buildHealthRecord,
  loadDetectionSettings,
  loadLinkHealthCache,
  persistLinkHealthCache,
  pruneExpiredHealthCache,
  saveCachedLinkHealth,
} from '@/utils/linkDetection';

const props = defineProps<{
  mergedResults: MergedResults;
  loading: boolean;
  hasSearched: boolean;
  isActivelySearching: boolean;
}>();

// 当前激活的标签
const activeTab = ref('');
// 当前标签页的数据
const currentTabData = ref<MergedResultItem[]>([]);
// 虚拟列表显示的数据
const visibleItems = ref<MergedResultItem[]>([]);
// 每次加载的数量
const PAGE_SIZE = 20;
// 当前加载的页码
const currentPage = ref(1);
const listContainerRef = ref<HTMLElement | null>(null);
// 当前查看详情的结果项
const detailItem = ref<MergedResultItem | null>(null);
const qrItem = ref<MergedResultItem | null>(null);
const qrDiskType = ref('');
const qrDataUrl = ref('');
const qrLoading = ref(false);
const qrError = ref('');
let qrRequestId = 0;
// 复制状态
const linkCopyStatus = ref<'idle' | 'success' | 'error'>('idle');
const passwordCopyStatus = ref<'idle' | 'success' | 'error'>('idle');
const linkCopyTimer = ref<number | null>(null);
const passwordCopyTimer = ref<number | null>(null);
const listPasswordFeedbackKey = ref('');
const listPasswordFeedbackStatus = ref<'idle' | 'success' | 'error'>('idle');
const listPasswordFeedbackTimer = ref<number | null>(null);
const detectionSettings = ref<DetectionSettings>(loadDetectionSettings());
const healthCache = ref<Record<string, LinkHealthRecord>>({});
const pendingHealthMap = ref<Record<string, true>>({});
const currentViewToken = ref('');

const supportedDetectionDiskTypes = new Set([
  'baidu',
  'quark',
  'aliyun',
  'uc',
  'tianyi',
  '123',
  'xunlei',
  '115',
  'mobile'
]);

let visibilityObserver: IntersectionObserver | null = null;
let flushTimer: number | null = null;
const queuedItems = new Map<string, LinkCheckItem>();
const inFlightKeys = new Set<string>();

// 计算所有可用的网盘类型
const diskTypes = computed(() => {
  return Object.keys(props.mergedResults || {}).sort((a, b) => {
    const countDifference = (props.mergedResults[b]?.length || 0) - (props.mergedResults[a]?.length || 0);
    return countDifference || a.localeCompare(b);
  });
});

// 判断是否有搜索结果
const hasResults = computed(() => {
  return diskTypes.value.length > 0;
});

// 判断是否显示空状态（无结果且已完成搜索）
const showEmptyState = computed(() => {
  return !hasResults.value && props.hasSearched && !props.isActivelySearching;
});

// 判断是否显示搜索中状态（无结果但正在搜索）
const showSearchingState = computed(() => {
  return !hasResults.value && props.hasSearched && props.isActivelySearching;
});

// 判断是否显示初始状态（未搜索）
const showInitialState = computed(() => {
  return !hasResults.value && !props.hasSearched;
});

const formatSource = (source?: string) => (source || '').replace(/^plugin:/i, '');

const hydrateHealthCache = () => {
  const nextCache = pruneExpiredHealthCache(loadLinkHealthCache());
  healthCache.value = nextCache;
  persistLinkHealthCache(nextCache);
};

const reloadDetectionSettings = () => {
  detectionSettings.value = loadDetectionSettings();
};

const getHealthStateKey = (diskType: string, url: string) => {
  return buildHealthCacheKey(diskType, url);
};

const getLinkHealthRecord = (diskType: string, url: string) => {
  const key = getHealthStateKey(diskType, url);

  if (pendingHealthMap.value[key]) {
    return buildHealthRecord('pending', {
      checked_at: Date.now(),
      expires_at: Date.now() + 5 * 60 * 1000
    });
  }

  return healthCache.value[key] || null;
};

const getIndicatorState = (item: MergedResultItem): LinkHealthState => {
  if (!activeTab.value) return 'idle';
  return getLinkHealthRecord(activeTab.value, item.url)?.state || 'idle';
};

const getIndicatorTitle = (item: MergedResultItem) => {
  const record = getLinkHealthRecord(activeTab.value, item.url);
  if (!record) return '未检测';

  const labelMap: Record<LinkHealthState, string> = {
    idle: '未检测',
    pending: '检测中',
    ok: '链接有效',
    bad: '链接失效',
    locked: '需要提取码',
    unsupported: '暂不支持检测',
    uncertain: '检测结果不确定'
  };

  return record.summary ? `${labelMap[record.state]}: ${record.summary}` : labelMap[record.state];
};

const shouldShowIndicator = (item: MergedResultItem) => {
  return getIndicatorState(item) !== 'idle';
};

const getIndicatorClass = (item: MergedResultItem) => {
  const state = getIndicatorState(item);
  return {
    'health-indicator': true,
    'is-pending': state === 'pending',
    'is-ok': state === 'ok',
    'is-bad': state === 'bad',
    'is-locked': state === 'locked',
    'is-uncertain': state === 'uncertain',
    'is-unsupported': state === 'unsupported'
  };
};

const getHealthPriority = (item: MergedResultItem) => {
  const state = activeTab.value
    ? getLinkHealthRecord(activeTab.value, item.url)?.state || 'idle'
    : 'idle';

  switch (state) {
    case 'ok':
      return 0;
    case 'locked':
      return 1;
    case 'pending':
      return 2;
    case 'idle':
      return 3;
    case 'unsupported':
      return 4;
    case 'uncertain':
      return 5;
    case 'bad':
      return 6;
    default:
      return 3;
  }
};

const getRankedTabData = () => {
  if (!activeTab.value || !props.mergedResults[activeTab.value]) {
    return [];
  }

  const source = props.mergedResults[activeTab.value] || [];
  if (!detectionSettings.value.enabled || !supportedDetectionDiskTypes.has(activeTab.value)) {
    return source;
  }

  return source
    .map((item, index) => ({
      item,
      index,
      priority: getHealthPriority(item)
    }))
    .sort((a, b) => {
      if (a.priority !== b.priority) {
        return a.priority - b.priority;
      }

      return a.index - b.index;
    })
    .map(({ item }) => item);
};

const clearPendingForKeys = (keys: string[]) => {
  if (!keys.length) return;

  const nextMap = { ...pendingHealthMap.value };
  keys.forEach((key) => {
    delete nextMap[key];
    inFlightKeys.delete(key);
  });
  pendingHealthMap.value = nextMap;
};

const resetInspectionQueue = () => {
  queuedItems.clear();
  if (flushTimer) {
    clearTimeout(flushTimer);
    flushTimer = null;
  }
  pendingHealthMap.value = {};
  inFlightKeys.clear();
  currentViewToken.value = `${activeTab.value || 'empty'}-${Date.now()}`;
};

const scheduleFlush = () => {
  if (flushTimer || !detectionSettings.value.enabled) return;

  flushTimer = window.setTimeout(() => {
    flushTimer = null;
    void flushInspectionQueue();
  }, 220);
};

const saveHealthResult = (diskType: string, url: string, record: LinkHealthRecord) => {
  saveCachedLinkHealth(diskType, url, record);
  healthCache.value = {
    ...healthCache.value,
    [getHealthStateKey(diskType, url)]: record
  };
};

const flushInspectionQueue = async () => {
  if (!detectionSettings.value.enabled || queuedItems.size === 0) return;

  const batchEntries = Array.from(queuedItems.entries()).slice(0, 6);
  batchEntries.forEach(([key]) => queuedItems.delete(key));

  const pendingMap = { ...pendingHealthMap.value };
  const items = batchEntries.map(([key, item]) => {
    pendingMap[key] = true;
    inFlightKeys.add(key);
    return item;
  });
  pendingHealthMap.value = pendingMap;

  try {
    const response = await inspectVisibleLinks(items, currentViewToken.value);

    response.results.forEach((result) => {
      const record = buildHealthRecord(result.state, {
        summary: result.summary,
        normalized_url: result.normalized_url,
        checked_at: result.checked_at,
        expires_at: result.expires_at
      });

      saveHealthResult(result.disk_type, result.url, record);
    });
  } catch (error) {
    const fallbackRecord = buildHealthRecord('uncertain', {
      summary: '检测服务暂不可用',
      checked_at: Date.now(),
      expires_at: Date.now() + 5 * 60 * 1000
    });

    items.forEach((item) => {
      saveHealthResult(item.disk_type, item.url, fallbackRecord);
    });
    console.error('链接检测失败:', error);
  } finally {
    clearPendingForKeys(batchEntries.map(([key]) => key));

    if (queuedItems.size > 0) {
      scheduleFlush();
    }
  }
};

const queueItemInspection = (item: MergedResultItem) => {
  if (!detectionSettings.value.enabled || !activeTab.value) return;
  if (!supportedDetectionDiskTypes.has(activeTab.value)) return;

  const key = getHealthStateKey(activeTab.value, item.url);
  if (queuedItems.has(key) || inFlightKeys.has(key)) return;

  const cached = healthCache.value[key];
  if (cached && cached.expires_at > Date.now()) return;

  queuedItems.set(key, {
    disk_type: activeTab.value,
    url: item.url,
    password: item.password
  });
  scheduleFlush();
};

const rebuildVisibilityObserver = () => {
  if (visibilityObserver) {
    visibilityObserver.disconnect();
    visibilityObserver = null;
  }

  if (!detectionSettings.value.enabled || !listContainerRef.value || !activeTab.value) {
    return;
  }

  visibilityObserver = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting || entry.intersectionRatio < 0.35) return;

        const index = Number((entry.target as HTMLElement).dataset.visibleIndex ?? '-1');
        if (Number.isNaN(index) || index < 0) return;

        const item = visibleItems.value[index];
        if (!item) return;

        queueItemInspection(item);
      });
    },
    {
      root: listContainerRef.value,
      threshold: [0.35, 0.6]
    }
  );

  listContainerRef.value
    .querySelectorAll<HTMLElement>('.result-item')
    .forEach((element) => visibilityObserver?.observe(element));
};

// 监听结果变化，智能选择标签
watch(
  () => props.mergedResults,
  (newVal) => {
    if (newVal && Object.keys(newVal).length > 0) {
      nextTick(() => {
        // 如果当前没有选中标签，或者当前选中的标签在新数据中不存在，则选择第一个标签
        const availableTypes = diskTypes.value;
        if (!activeTab.value || !availableTypes.includes(activeTab.value)) {
          activeTab.value = availableTypes[0] || '';
        }
        updateCurrentTabData();
        resetInspectionQueue();
      });
    } else {
      // 当没有结果时，清空当前数据
      activeTab.value = '';
      currentTabData.value = [];
      visibleItems.value = [];
      resetInspectionQueue();
    }
  },
  { immediate: true, deep: true }
);

// 监听标签页切换
watch(
  () => activeTab.value,
  () => {
    currentPage.value = 1;
    updateCurrentTabData();
    resetInspectionQueue();
  }
);

watch(
  () => detectionSettings.value.enabled,
  (enabled) => {
    if (!enabled) {
      resetInspectionQueue();
      updateCurrentTabData();
      return;
    }

    updateCurrentTabData();
  }
);

watch(
  () => [
    activeTab.value,
    detectionSettings.value.enabled,
    Object.keys(pendingHealthMap.value).sort().join('|'),
    Object.entries(healthCache.value)
      .map(([key, record]) => `${key}:${record.state}:${record.checked_at}`)
      .sort()
      .join('|')
  ],
  () => {
    if (!activeTab.value) return;
    updateCurrentTabData();
  }
);

watch(
  () => [
    activeTab.value,
    detectionSettings.value.enabled,
    visibleItems.value.map((item) => `${item.url}|${item.password || ''}`).join('||')
  ],
  () => {
    nextTick(() => {
      rebuildVisibilityObserver();
    });
  }
);

// 更新当前标签页数据
const updateCurrentTabData = () => {
  if (!activeTab.value || !props.mergedResults[activeTab.value]) {
    currentTabData.value = [];
    visibleItems.value = [];
    return;
  }
  
  currentTabData.value = getRankedTabData();
  loadMoreItems();
};

// 加载更多数据
const loadMoreItems = () => {
  const start = 0;
  const end = currentPage.value * PAGE_SIZE;
  visibleItems.value = currentTabData.value.slice(start, end);
};

// 处理滚动加载更多
const handleScroll = (e: Event) => {
  const target = e.target as HTMLElement;
  const scrollBottom = target.scrollHeight - target.scrollTop - target.clientHeight;
  
  // 当滚动到底部100px时，加载更多数据
  if (scrollBottom < 100 && visibleItems.value.length < currentTabData.value.length) {
    currentPage.value++;
    loadMoreItems();
  }
};

const getLinkHost = (url: string) => {
  try {
    return new URL(url).hostname.replace(/^www\./, '');
  } catch {
    return '资源链接';
  }
};

// 通用复制函数（支持降级处理）
const copyToClipboard = async (text: string): Promise<boolean> => {
  // 方法1: 优先使用现代 Clipboard API（安全上下文可用）
  if (window.isSecureContext && navigator.clipboard && navigator.clipboard.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (err) {
      console.warn('Clipboard API 失败，尝试降级方案:', err);
    }
  }
  
  // 方法2: 降级使用传统 execCommand 方法 (兼容HTTP)
  try {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', 'true');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    textarea.style.left = '-9999px';
    document.body.appendChild(textarea);
    
    textarea.focus();
    textarea.select();
    textarea.setSelectionRange(0, text.length);
    
    const successful = document.execCommand('copy');
    document.body.removeChild(textarea);
    
    return successful;
  } catch (err) {
    console.error('复制失败:', err);
    return false;
  }
};

const clearCopyFeedbackTimers = () => {
  if (linkCopyTimer.value) {
    clearTimeout(linkCopyTimer.value);
    linkCopyTimer.value = null;
  }

  if (passwordCopyTimer.value) {
    clearTimeout(passwordCopyTimer.value);
    passwordCopyTimer.value = null;
  }
};

const clearListPasswordFeedbackTimer = () => {
  if (listPasswordFeedbackTimer.value) {
    clearTimeout(listPasswordFeedbackTimer.value);
    listPasswordFeedbackTimer.value = null;
  }
};

const resetCopyStatus = () => {
  clearCopyFeedbackTimers();
  linkCopyStatus.value = 'idle';
  passwordCopyStatus.value = 'idle';
};

const getResultItemKey = (item: MergedResultItem, index: number) => {
  return `${index}-${item.url}-${item.password ?? ''}`;
};

const getListPasswordStatus = (item: MergedResultItem, index: number) => {
  if (listPasswordFeedbackKey.value !== getResultItemKey(item, index)) {
    return 'idle';
  }

  return listPasswordFeedbackStatus.value;
};

const copyListEntry = async (item: MergedResultItem, index: number) => {
  const content = [
    item.note,
    item.url,
    item.password ? `提取码：${item.password}` : ''
  ].filter(Boolean).join('\n');
  const success = await copyToClipboard(content);

  clearListPasswordFeedbackTimer();
  listPasswordFeedbackKey.value = getResultItemKey(item, index);
  listPasswordFeedbackStatus.value = success ? 'success' : 'error';
  listPasswordFeedbackTimer.value = window.setTimeout(() => {
    listPasswordFeedbackKey.value = '';
    listPasswordFeedbackStatus.value = 'idle';
    listPasswordFeedbackTimer.value = null;
  }, 1800);
};

const setCopyStatus = (type: 'link' | 'password', success: boolean) => {
  const status = success ? 'success' : 'error';
  const timerRef = type === 'link' ? linkCopyTimer : passwordCopyTimer;
  const statusRef = type === 'link' ? linkCopyStatus : passwordCopyStatus;

  if (timerRef.value) {
    clearTimeout(timerRef.value);
  }

  statusRef.value = status;
  timerRef.value = window.setTimeout(() => {
    statusRef.value = 'idle';
    timerRef.value = null;
  }, 1800);
};

const openTitleDetail = (item: MergedResultItem) => {
  detailItem.value = item;
  resetCopyStatus();
};

const closeTitleDetail = () => {
  detailItem.value = null;
  resetCopyStatus();
};

const qrInstruction = computed(() => {
  if (['magnet', 'ed2k', 'other'].includes(qrDiskType.value)) {
    return '使用手机扫码打开资源链接';
  }

  return `使用${getDiskTypeName(qrDiskType.value)}网盘 App 扫码保存`;
});

const buildQrContent = (item: MergedResultItem) => {
  return [
    item.url,
    item.password ? `提取码：${item.password}` : ''
  ].filter(Boolean).join('\n');
};

const openQrCode = async (item: MergedResultItem) => {
  const requestId = ++qrRequestId;
  resetCopyStatus();
  qrItem.value = item;
  qrDiskType.value = activeTab.value;
  qrDataUrl.value = '';
  qrError.value = '';
  qrLoading.value = true;

  try {
    const dataUrl = await QRCode.toDataURL(buildQrContent(item), {
      width: 360,
      margin: 2,
      errorCorrectionLevel: 'M',
      color: {
        dark: '#102a25',
        light: '#ffffff'
      }
    });

    if (requestId === qrRequestId) {
      qrDataUrl.value = dataUrl;
    }
  } catch (error) {
    console.error('二维码生成失败:', error);
    if (requestId === qrRequestId) {
      qrError.value = '二维码生成失败，请复制链接后重试';
    }
  } finally {
    if (requestId === qrRequestId) {
      qrLoading.value = false;
    }
  }
};

const closeQrCode = () => {
  qrRequestId++;
  qrItem.value = null;
  qrDiskType.value = '';
  qrDataUrl.value = '';
  qrError.value = '';
  qrLoading.value = false;
  resetCopyStatus();
};

const copyQrLink = async () => {
  if (!qrItem.value) return;
  const success = await copyToClipboard(buildQrContent(qrItem.value));
  setCopyStatus('link', success);
};

const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && qrItem.value) {
    closeQrCode();
  } else if (event.key === 'Escape' && detailItem.value) {
    closeTitleDetail();
  }
};

const copyDetailField = async (type: 'link' | 'password') => {
  if (!detailItem.value) return;

  const text = type === 'link' ? detailItem.value.url : detailItem.value.password;
  if (!text) return;

  const success = await copyToClipboard(text);
  setCopyStatus(type, success);
};

const getCopyButtonText = (type: 'link' | 'password') => {
  const status = type === 'link' ? linkCopyStatus.value : passwordCopyStatus.value;

  if (status === 'success') {
    return '复制成功';
  }

  if (status === 'error') {
    return '复制失败';
  }

  return type === 'link' ? '复制链接' : '复制密码';
};

const getDetailPasswordText = () => {
  if (passwordCopyStatus.value === 'success') {
    return '复制成功';
  }

  if (passwordCopyStatus.value === 'error') {
    return '复制失败';
  }

  return detailItem.value?.password ?? '';
};

// 格式化日期时间
const formatDateTime = (dateTimeStr?: string) => {
  if (!dateTimeStr) return '';
  
  try {
    const date = new Date(dateTimeStr);
    return date.toLocaleString('zh-CN', { 
      year: 'numeric', 
      month: '2-digit', 
      day: '2-digit'
    });
  } catch (e) {
    return dateTimeStr;
  }
};

// 获取网盘类型中文名称
const getDiskName = (type: string) => {
  return getDiskTypeName(type);
};

watch([detailItem, qrItem], ([newDetail, newQr]) => {
  document.body.style.overflow = newDetail || newQr ? 'hidden' : '';
});

onMounted(() => {
  hydrateHealthCache();
  reloadDetectionSettings();
  window.addEventListener('keydown', handleKeydown);
  window.addEventListener('storage', reloadDetectionSettings);
  window.addEventListener('config:saved', reloadDetectionSettings);
  nextTick(() => {
    rebuildVisibilityObserver();
  });
});

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown);
  window.removeEventListener('storage', reloadDetectionSettings);
  window.removeEventListener('config:saved', reloadDetectionSettings);
  document.body.style.overflow = '';
  clearCopyFeedbackTimers();
  clearListPasswordFeedbackTimer();
  if (visibilityObserver) {
    visibilityObserver.disconnect();
    visibilityObserver = null;
  }
  resetInspectionQueue();
});

</script>

<template>
  <div class="results-wrapper">
    <!-- 初始状态 -->
    <div v-if="showInitialState" class="empty-state">
      <div class="initial-mark" aria-hidden="true">
        <span></span><span></span><span></span>
      </div>
      <p class="empty-title">输入名称开始搜索</p>
      <p class="empty-subtitle">电影、剧集、软件和学习资料均可检索</p>
      <div class="initial-tips">
        <span><kbd>Enter</kbd> 搜索</span>
        <span>结果自动按网盘归类</span>
      </div>
    </div>
    
    <!-- 搜索中状态 -->
    <div v-else-if="showSearchingState" class="searching-state">
      <div class="searching-icon">
        <div class="searching-spinner"></div>
      </div>
      <p class="searching-title">正在搜索资源中...</p>
      <p class="searching-subtitle">资源搜索可能需要一些时间，请耐心等待</p>
    </div>
    
    <!-- 搜索无结果状态 -->
    <div v-else-if="showEmptyState" class="empty-state">
      <div class="empty-icon">
        <svg class="icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.172 16.172a4 4 0 015.656 0M9 10h.01M15 10h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path>
        </svg>
      </div>
      <p class="empty-title">未找到相关资源</p>
      <p class="empty-subtitle">请尝试其他关键词</p>
    </div>
    
    <!-- 搜索结果 -->
    <div v-else-if="hasResults" class="results-container">
      <!-- 标签页 -->
      <div class="tabs">
        <button 
          v-for="type in diskTypes" 
          :key="type"
          class="tab-button"
          :class="{ active: activeTab === type }"
          @click="activeTab = type"
        >
          <span>{{ getDiskName(type) }}</span>
          <small>{{ mergedResults[type]?.length || 0 }}</small>
        </button>
      </div>
      
      <!-- 内容区域 -->
      <div class="tab-content">
        <div v-if="!currentTabData.length" class="empty-tab">
          <p>暂无数据</p>
        </div>
        
        <div ref="listContainerRef" v-else class="result-list" @scroll="handleScroll">
          <div 
            v-for="(item, index) in visibleItems" 
            :key="getResultItemKey(item, index)"
            class="result-item"
            :data-visible-index="index"
          >
            <span class="result-rank">{{ index + 1 }}</span>
            <div class="result-content">
              <button
                type="button"
                class="result-title-button"
                :title="item.note"
                @click="openTitleDetail(item)"
              >
                <span class="result-title-row">
                  <span class="result-title">{{ item.note }}</span>
                  <span
                    v-if="shouldShowIndicator(item)"
                    :class="getIndicatorClass(item)"
                    :title="getIndicatorTitle(item)"
                  ></span>
                </span>
              </button>
              <div class="result-meta" v-if="item.source || item.datetime || item.password">
                <span v-if="item.source" class="result-source">{{ formatSource(item.source) }}</span>
                <span v-if="item.source && item.datetime" class="meta-separator">·</span>
                <span v-if="item.datetime" class="result-date">{{ formatDateTime(item.datetime) }}</span>
                <span v-if="item.source || item.datetime" class="meta-separator">·</span>
                <span class="result-host">{{ getLinkHost(item.url) }}</span>
                <span v-if="item.password" class="result-code">提取码 {{ item.password }}</span>
              </div>
            </div>
            
            <div class="result-actions">
              <button
                type="button"
                class="result-action result-copy"
                :class="{
                  copied: getListPasswordStatus(item, index) === 'success',
                  'copy-failed': getListPasswordStatus(item, index) === 'error'
                }"
                :title="item.password ? '复制标题、链接和提取码' : '复制标题和链接'"
                @click="copyListEntry(item, index)"
              >
                <template v-if="getListPasswordStatus(item, index) === 'success'">
                  复制成功
                </template>
                <template v-else-if="getListPasswordStatus(item, index) === 'error'">
                  复制失败
                </template>
                <template v-else>
                  复制
                </template>
              </button>
              <button
                type="button"
                class="result-action result-qr"
                title="显示链接二维码"
                @click="openQrCode(item)"
              >扫码</button>
              <a
                class="result-action result-open"
                :href="item.url"
                target="_blank"
                rel="noopener noreferrer"
                title="在浏览器中打开资源链接"
              >打开</a>
            </div>
          </div>
          
          <div v-if="visibleItems.length < currentTabData.length" class="loading-more">
            <div class="loading-spinner"></div>
            <span>加载更多...</span>
          </div>
        </div>
      </div>
      
      <!-- 持续搜索提示 -->
      <div v-if="isActivelySearching" class="ongoing-search-hint">
        <div class="hint-spinner"></div>
        <span>正在持续搜索更多资源...</span>
      </div>
    </div>

    <Teleport to="body">
      <Transition name="detail-fade">
        <div v-if="detailItem" class="detail-overlay" @click="closeTitleDetail">
          <div class="detail-dialog" @click.stop>
            <div class="detail-header">
              <div class="detail-heading">
                <p class="detail-label">完整标题</p>
                <h3 class="detail-title">{{ detailItem.note }}</h3>
              </div>
              <button
                type="button"
                class="detail-close"
                aria-label="关闭标题详情"
                @click="closeTitleDetail"
              >
                <svg class="detail-close-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path>
                </svg>
              </button>
            </div>

            <div v-if="detailItem.source || detailItem.datetime" class="detail-meta">
              <span v-if="detailItem.source" class="result-source">{{ formatSource(detailItem.source) }}</span>
              <span v-if="detailItem.source && detailItem.datetime" class="meta-separator">·</span>
              <span v-if="detailItem.datetime" class="result-date">{{ formatDateTime(detailItem.datetime) }}</span>
            </div>

            <div class="detail-actions">
              <button
                type="button"
                class="detail-copy-btn"
                :class="{
                  success: linkCopyStatus === 'success',
                  error: linkCopyStatus === 'error'
                }"
                @click="copyDetailField('link')"
              >
                {{ getCopyButtonText('link') }}
              </button>

              <button
                v-if="detailItem.password"
                type="button"
                class="detail-password-action"
                :class="{
                  success: passwordCopyStatus === 'success',
                  error: passwordCopyStatus === 'error'
                }"
                @click="copyDetailField('password')"
              >
                <template v-if="passwordCopyStatus === 'idle'">
                  <span class="detail-password-label">提取码:</span>
                  <span class="detail-password-content">{{ detailItem.password }}</span>
                </template>
                <template v-else>
                  {{ getDetailPasswordText() }}
                </template>
              </button>
            </div>

            <a
              class="detail-link-preview"
              :href="detailItem.url"
              :title="detailItem.url"
              target="_blank"
              rel="noopener noreferrer"
            >
              {{ detailItem.url }}
            </a>
          </div>
        </div>
      </Transition>
    </Teleport>

    <Teleport to="body">
      <Transition name="detail-fade">
        <div v-if="qrItem" class="detail-overlay" @click="closeQrCode">
          <div class="qr-dialog" role="dialog" aria-modal="true" aria-labelledby="qr-dialog-title" @click.stop>
            <div class="qr-header">
              <div>
                <p class="detail-label">手机扫码</p>
                <h3 id="qr-dialog-title" class="qr-title">{{ qrInstruction }}</h3>
              </div>
              <button
                type="button"
                class="detail-close"
                aria-label="关闭二维码"
                @click="closeQrCode"
              >
                <svg class="detail-close-icon" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"></path>
                </svg>
              </button>
            </div>

            <div class="qr-body">
              <div class="qr-code-frame" :class="{ 'is-loading': qrLoading }">
                <div v-if="qrLoading" class="qr-loading">
                  <span class="loading-spinner"></span>
                  <span>正在生成</span>
                </div>
                <img v-else-if="qrDataUrl" :src="qrDataUrl" alt="网盘分享链接二维码" class="qr-code-image" />
                <p v-else class="qr-error">{{ qrError }}</p>
              </div>

              <p class="qr-resource-title" :title="qrItem.note">{{ qrItem.note }}</p>
              <div v-if="qrItem.password" class="qr-password">
                <span>提取码</span>
                <strong>{{ qrItem.password }}</strong>
              </div>
              <p class="qr-privacy">
                {{ qrItem.password ? '二维码已包含分享链接和提取码' : '二维码已包含分享链接' }}，内容不会发送给第三方。
              </p>
            </div>

            <div class="qr-actions">
              <button
                type="button"
                class="qr-secondary-action"
                :class="{ success: linkCopyStatus === 'success', error: linkCopyStatus === 'error' }"
                @click="copyQrLink"
              >{{ getCopyButtonText('link') }}</button>
              <a
                class="qr-primary-action"
                :href="qrItem.url"
                target="_blank"
                rel="noopener noreferrer"
              >浏览器打开</a>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<style scoped>
.results-wrapper {
  width: 100%;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
}

.empty-state, .searching-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 2.75rem 1rem;
  text-align: center;
  background-color: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: 1.1rem;
  box-shadow: 0 12px 36px hsl(var(--shadow-color) / 0.06);
}

.empty-icon {
  width: 3.75rem;
  height: 3.75rem;
  background: linear-gradient(135deg, hsl(var(--primary) / 0.14), hsl(var(--accent-secondary) / 0.1));
  border: 1px solid hsl(var(--primary) / 0.12);
  border-radius: 1.15rem;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 1rem;
}

.empty-icon .icon {
  width: 2rem;
  height: 2rem;
  color: hsl(var(--primary));
}

.searching-icon {
  width: 4rem;
  height: 4rem;
  background-color: hsl(var(--primary) / 0.14);
  border-radius: 9999px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 1rem;
}

.searching-spinner {
  width: 2rem;
  height: 2rem;
  border: 3px solid hsl(var(--border));
  border-top-color: hsl(var(--primary));
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

.empty-title, .searching-title {
  font-size: 1.125rem;
  font-weight: 700;
  color: hsl(var(--foreground));
  margin-bottom: 0.5rem;
}

.feature-list {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 0.6rem;
  margin-top: 1.15rem;
}

.feature-list span {
  display: inline-flex;
  align-items: center;
  gap: 0.38rem;
  padding: 0.34rem 0.62rem;
  border: 1px solid hsl(var(--border));
  border-radius: 999px;
  background: hsl(var(--muted) / 0.55);
  color: hsl(var(--muted-foreground));
  font-size: 0.72rem;
  font-weight: 600;
}

.feature-dot {
  width: 0.38rem;
  height: 0.38rem;
  border-radius: 50%;
}

.feature-dot-blue { background: #6366f1; }
.feature-dot-green { background: #22c55e; }
.feature-dot-purple { background: #a855f7; }

.empty-subtitle, .searching-subtitle {
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
}

.results-container {
  width: 100%;
  min-width: 0;
  max-width: 100%;
  background-color: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: 1rem;
  box-shadow: 0 12px 34px hsl(var(--shadow-color) / 0.06);
  overflow: hidden;
}

.tabs {
  display: flex;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: none;
  overscroll-behavior-x: contain;
  border-bottom: 1px solid hsl(var(--border));
  background-color: hsl(var(--muted) / 0.55);
  padding: 0.45rem;
  gap: 0.25rem;
}

.tabs::-webkit-scrollbar {
  display: none;
}

.tab-button {
  padding: 0.52rem 0.8rem;
  white-space: nowrap;
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
  background: transparent;
  border: none;
  cursor: pointer;
  border-radius: 0.55rem;
  transition: all 0.2s ease;
}

.tab-button:hover {
  color: hsl(var(--primary));
}

.tab-button.active {
  color: hsl(var(--primary));
  font-weight: 650;
  background: hsl(var(--card));
  box-shadow: 0 1px 4px hsl(var(--shadow-color) / 0.08), inset 0 0 0 1px hsl(var(--border));
}

.tab-button.active::after {
  display: none;
}

.tab-content {
  width: 100%;
  min-width: 0;
  max-width: 100%;
  min-height: 300px;
  overflow: hidden;
}

.empty-tab {
  padding: 3rem 1rem;
  text-align: center;
  color: hsl(var(--muted-foreground));
}

.result-list {
  width: 100%;
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
  max-height: 600px;
  overflow-x: hidden;
  overflow-y: auto;
  padding: 0.55rem;
}

.result-item {
  min-width: 0;
  max-width: 100%;
  box-sizing: border-box;
  margin-bottom: 0.28rem;
  padding: 0.78rem 0.85rem;
  border: 1px solid transparent;
  border-radius: 0.72rem;
  transition: background-color 0.2s ease, border-color 0.2s ease, transform 0.2s ease;
}

.result-item:hover {
  background-color: hsl(var(--primary) / 0.045);
  border-color: hsl(var(--primary) / 0.12);
  transform: translateY(-1px);
}

.result-row {
  display: flex;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.5rem;
}

.result-row:last-child {
  margin-bottom: 0;
}

.result-title-button {
  appearance: none;
  border: none;
  background: transparent;
  padding: 0;
  margin: 0;
  width: 100%;
  min-width: 0;
  text-align: left;
  font: inherit;
  color: inherit;
  cursor: pointer;
}

.result-title-button:focus-visible {
  outline: 2px solid hsl(var(--primary));
  outline-offset: 2px;
  border-radius: 0.25rem;
}

.result-title {
  display: block;
  width: 100%;
  font-size: 0.95rem;
  font-weight: 630;
  color: hsl(var(--foreground));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.result-title-row {
  display: flex;
  align-items: center;
  width: 100%;
  max-width: 100%;
  min-width: 0;
  overflow: hidden;
  gap: 0.45rem;
}

.health-indicator {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 9999px;
  flex: 0 0 auto;
  background: #cbd5e1;
}

.health-indicator.is-pending {
  background: #60a5fa;
  animation: pulse-dot 1.1s ease-in-out infinite;
}

.health-indicator.is-ok {
  background: #22c55e;
}

.health-indicator.is-bad {
  background: #ef4444;
}

.health-indicator.is-locked {
  background: #f59e0b;
}

.health-indicator.is-uncertain,
.health-indicator.is-unsupported {
  background: #94a3b8;
}

/* 标题行布局 */
.result-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
}

/* 桌面端：数据来源与标题同行 */
.result-meta.desktop-only {
  display: flex;
  align-items: center;
  margin-left: 0.75rem;
  white-space: nowrap;
  flex-shrink: 0;
}

/* 移动端：数据来源单独一行 */
.result-meta.mobile-only {
  display: none;
  align-items: center;
  margin-top: 0.375rem;
  margin-bottom: 0.25rem;
}

/* 响应式显示控制 */
@media (max-width: 768px) {
  .desktop-only {
    display: none !important;
  }
  
  .mobile-only {
    display: flex !important;
  }
  
  .result-header {
    flex-direction: column;
    align-items: flex-start;
  }
  
  .result-title {
    width: 100%;
    margin-bottom: 0;
  }
  
  /* 移动端数据来源标签优化 */
  .result-source {
    font-size: 0.6875rem;
    padding: 0.0625rem 0.25rem;
  }
}

.result-source {
  font-size: 0.75rem;
  color: hsl(var(--primary));
  font-weight: 500;
  background-color: hsl(var(--primary) / 0.14);
  padding: 0.125rem 0.375rem;
  border-radius: 0.25rem;
  border: 1px solid hsl(var(--primary) / 0.35);
}

.meta-separator {
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
  margin: 0 0.375rem;
}

.result-date {
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
}

.result-link {
  display: block;
  min-width: 0;
  max-width: 100%;
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
  flex: 1;
}

.result-link:hover {
  color: hsl(var(--primary));
  text-decoration: underline;
}

.result-password {
  appearance: none;
  border: none;
  background: transparent;
  padding: 0;
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
  margin-left: 0.75rem;
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.2s ease;
}

.result-password:hover {
  color: hsl(var(--muted-foreground));
}

.result-password:focus-visible {
  outline: 2px solid hsl(var(--primary));
  outline-offset: 2px;
  border-radius: 0.25rem;
}

.result-password.copied {
  color: #059669;
  display: inline-flex;
  align-items: center;
  padding: 0.3rem 0.7rem;
  border-radius: 9999px;
  background: #ecfdf5;
  border: 1px solid #d1fae5;
  line-height: 1;
}

.result-password.copy-failed {
  color: #dc2626;
}

.password-value {
  color: #10b981;
  font-weight: 500;
}

.detail-overlay {
  position: fixed;
  inset: 0;
  z-index: 10000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
  background: rgba(15, 23, 42, 0.45);
  backdrop-filter: blur(8px);
}

.detail-dialog {
  width: min(100%, 640px);
  max-height: min(80vh, 680px);
  overflow-y: auto;
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: 1rem;
  box-shadow: 0 24px 64px rgba(15, 23, 42, 0.2);
}

.qr-dialog {
  width: min(100%, 390px);
  max-height: min(92vh, 650px);
  overflow-y: auto;
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: 1rem;
  box-shadow: 0 24px 64px rgba(15, 23, 42, 0.2);
}

.qr-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  padding: 1.1rem 1.1rem 0;
}

.qr-title {
  margin: 0;
  color: hsl(var(--foreground));
  font-size: 1rem;
  font-weight: 650;
}

.qr-body {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 1rem 1.1rem 0;
}

.qr-code-frame {
  width: min(100%, 260px);
  aspect-ratio: 1;
  display: grid;
  place-items: center;
  padding: 0.6rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.75rem;
  background: #fff;
}

.qr-code-frame.is-loading {
  background: hsl(var(--muted) / 0.4);
}

.qr-code-image {
  display: block;
  width: 100%;
  height: 100%;
}

.qr-loading {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  color: hsl(var(--muted-foreground));
  font-size: 0.75rem;
}

.qr-error {
  margin: 0;
  color: hsl(var(--destructive));
  font-size: 0.75rem;
  text-align: center;
}

.qr-resource-title {
  width: 100%;
  margin: 0.8rem 0 0;
  overflow: hidden;
  color: hsl(var(--foreground));
  font-size: 0.8rem;
  font-weight: 600;
  text-align: center;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.qr-password {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  margin-top: 0.55rem;
  padding: 0.3rem 0.55rem;
  border-radius: 0.4rem;
  background: hsl(var(--success) / 0.09);
  color: hsl(var(--muted-foreground));
  font-size: 0.72rem;
}

.qr-password strong {
  color: hsl(var(--success));
  font-size: 0.82rem;
  letter-spacing: 0.08em;
}

.qr-privacy {
  margin: 0.7rem 0 0;
  color: hsl(var(--muted-foreground));
  font-size: 0.66rem;
  text-align: center;
}

.qr-actions {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.55rem;
  padding: 1rem 1.1rem 1.1rem;
}

.qr-secondary-action,
.qr-primary-action {
  height: 2.3rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.45rem;
  cursor: pointer;
  font-size: 0.78rem;
  font-weight: 620;
  text-decoration: none;
}

.qr-secondary-action {
  border: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  color: hsl(var(--foreground));
}

.qr-secondary-action:hover { background: hsl(var(--muted)); }
.qr-secondary-action.success { color: hsl(var(--success)); }
.qr-secondary-action.error { color: hsl(var(--destructive)); }

.qr-primary-action {
  border: 1px solid hsl(var(--primary));
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
}

.qr-primary-action:hover { background: hsl(var(--primary) / 0.9); }

.detail-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  padding: 1.25rem 1.25rem 0.75rem;
}

.detail-heading {
  min-width: 0;
}

.detail-label {
  margin: 0 0 0.5rem;
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: hsl(var(--muted-foreground));
}

.detail-title {
  margin: 0;
  font-size: 1rem;
  line-height: 1.7;
  font-weight: 600;
  color: hsl(var(--foreground));
  word-break: break-word;
}

.detail-close {
  appearance: none;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  color: hsl(var(--muted-foreground));
  width: 2rem;
  height: 2rem;
  border-radius: 9999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  transition: all 0.2s ease;
}

.detail-close:hover {
  color: hsl(var(--foreground));
  border-color: #cbd5e1;
  background: #f8fafc;
}

.detail-close:focus-visible,
.detail-copy-btn:focus-visible,
.detail-password-action:focus-visible,
.detail-link-preview:focus-visible {
  outline: 2px solid hsl(var(--primary));
  outline-offset: 2px;
}

.detail-close-icon {
  width: 1rem;
  height: 1rem;
}

.detail-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.375rem;
  padding: 0 1.25rem 1rem;
}

.detail-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0 1.25rem 0.875rem;
  min-width: 0;
}

.detail-password-action {
  appearance: none;
  border: none;
  background: transparent;
  padding: 0;
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  align-items: center;
  gap: 0.375rem;
  min-width: 0;
  color: hsl(var(--muted-foreground));
}

.detail-password-action:hover {
  color: #047857;
}

.detail-password-action.success {
  color: #059669;
  padding: 0.3rem 0.7rem;
  border-radius: 9999px;
  background: #ecfdf5;
  border: 1px solid #d1fae5;
  line-height: 1;
}

.detail-password-action.error {
  color: #dc2626;
}

.detail-copy-btn {
  appearance: none;
  border: none;
  background: transparent;
  color: hsl(var(--primary));
  padding: 0;
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
  flex-shrink: 0;
}

.detail-copy-btn:hover {
  color: hsl(var(--primary));
}

.detail-copy-btn.success {
  color: #059669;
  padding: 0.3rem 0.7rem;
  border-radius: 9999px;
  background: #ecfdf5;
  border: 1px solid #d1fae5;
  line-height: 1;
}

.detail-copy-btn.error {
  color: #dc2626;
}

.detail-password-label {
  color: inherit;
  flex-shrink: 0;
}

.detail-password-content {
  color: #10b981;
  max-width: 9rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-link-preview {
  appearance: none;
  display: block;
  width: calc(100% - 2.5rem);
  margin: 0 1.25rem 1.25rem;
  padding: 0;
  text-align: left;
  border: none;
  background: transparent;
  cursor: pointer;
  color: hsl(var(--primary));
  font-size: 0.875rem;
  line-height: 1.25rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: all 0.2s ease;
}

.detail-link-preview:hover {
  text-decoration: underline;
}

.detail-fade-enter-active,
.detail-fade-leave-active {
  transition: opacity 0.2s ease;
}

.detail-fade-enter-from,
.detail-fade-leave-to {
  opacity: 0;
}

.loading-more {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 1rem;
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}

.loading-spinner, .hint-spinner {
  width: 1rem;
  height: 1rem;
  border: 2px solid hsl(var(--border));
  border-top-color: hsl(var(--primary));
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.ongoing-search-hint {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 0.75rem;
  background-color: hsl(var(--primary) / 0.14);
  color: hsl(var(--primary));
  font-size: 0.875rem;
  border-top: 1px solid hsl(var(--border));
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@keyframes pulse-dot {
  0%,
  100% {
    opacity: 0.55;
    transform: scale(0.95);
  }
  50% {
    opacity: 1;
    transform: scale(1.08);
  }
}

@media (max-width: 768px) {
  .results-wrapper {
    width: 100%;
    display: flex;
    flex-direction: column;
    flex: 1 1 auto;
    min-height: 0;
  }

  .results-container {
    display: flex;
    flex-direction: column;
    flex: 1 1 auto;
    min-height: 0;
    overflow: hidden;
  }

  .tabs {
    flex: 0 0 auto;
  }

  .tab-content {
    display: flex;
    flex-direction: column;
    flex: 1 1 auto;
    min-height: 0;
    overflow: hidden;
  }

  .result-list {
    flex: 1 1 auto;
    max-height: none;
    min-height: 0;
    padding: 0.5rem;
    overflow-y: auto;
    scroll-padding-bottom: 1rem;
  }
  
  .result-item {
    padding: 0.75rem 0.5rem;
  }
  
  .tab-button {
    padding: 0.5rem 0.75rem;
    font-size: 0.75rem;
  }

  .detail-dialog {
    width: 100%;
    max-height: 85vh;
    border-radius: 1rem;
  }

  .detail-header {
    padding: 1rem 1rem 0.75rem;
  }

  .detail-meta,
  .detail-actions {
    padding-left: 1rem;
    padding-right: 1rem;
  }

  .detail-link-preview {
    width: calc(100% - 2rem);
    margin-left: 1rem;
    margin-right: 1rem;
  }
}

/* fnOS 小窗口紧凑布局 */
.empty-state,
.searching-state {
  padding: 1.5rem 1rem;
  border-radius: 0.5rem;
  box-shadow: none;
}

.empty-icon {
  width: 2.5rem;
  height: 2.5rem;
  margin-bottom: 0.625rem;
  border: 0;
  border-radius: 50%;
  background: hsl(var(--muted));
}

.empty-icon .icon {
  width: 1.25rem;
  height: 1.25rem;
}

.empty-title,
.searching-title {
  margin-bottom: 0.25rem;
  font-size: 0.875rem;
  font-weight: 600;
}

.empty-subtitle,
.searching-subtitle {
  font-size: 0.75rem;
}

.results-container {
  border-radius: 0.5rem;
  box-shadow: none;
}

.tabs {
  gap: 0.125rem;
  padding: 0.25rem 0.4rem;
}

.tab-button {
  padding: 0.4rem 0.65rem;
  border-radius: 0.35rem;
  font-size: 0.8125rem;
}

.tab-button.active {
  box-shadow: inset 0 0 0 1px hsl(var(--border));
}

.tab-content {
  min-height: 180px;
}

.result-list {
  max-height: calc(100vh - 10.5rem);
  padding: 0.35rem;
}

.result-item {
  margin-bottom: 0.125rem;
  padding: 0.55rem 0.65rem;
  border-radius: 0.4rem;
}

/* 1.1 信息密度：结果主体和操作区固定分栏，完整 URL 收进详情。 */
.results-wrapper {
  container-type: inline-size;
}

.results-container {
  border-radius: 0.55rem;
  background: hsl(var(--card));
}

.tabs {
  min-height: 2.35rem;
  padding: 0 0.35rem;
  gap: 0;
  background: hsl(var(--card));
}

.tab-button {
  position: relative;
  padding: 0.55rem 0.7rem 0.48rem;
  border-radius: 0;
  font-size: 0.8rem;
}

.tab-button.active {
  background: transparent;
  box-shadow: none;
}

.tab-button.active::after {
  display: block;
  content: '';
  position: absolute;
  right: 0.7rem;
  bottom: 0;
  left: 0.7rem;
  height: 2px;
  background: hsl(var(--primary));
}

.result-list {
  padding: 0;
}

.result-item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.75rem;
  min-height: 3.25rem;
  margin: 0;
  padding: 0.45rem 0.65rem;
  border: 0;
  border-bottom: 1px solid hsl(var(--border) / 0.65);
  border-radius: 0;
  transition: background-color 0.14s ease;
}

.result-item:last-child {
  border-bottom: 0;
}

.result-item:hover {
  border-color: hsl(var(--border) / 0.65);
  background: hsl(var(--muted) / 0.55);
  transform: none;
}

.result-content {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.22rem;
}

.result-title {
  font-size: 0.875rem;
  line-height: 1.3;
  font-weight: 560;
}

.result-meta {
  display: flex;
  align-items: center;
  gap: 0.32rem;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  color: hsl(var(--muted-foreground));
  font-size: 0.6875rem;
}

.result-source {
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  color: hsl(var(--muted-foreground));
  font-size: inherit;
  font-weight: 500;
}

.meta-separator {
  margin: 0;
  font-size: inherit;
}

.result-date,
.result-host {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: inherit;
}

.result-code {
  flex: 0 0 auto;
  margin-left: 0.2rem;
  color: hsl(var(--success));
  font-weight: 600;
}

.result-actions {
  display: flex;
  align-items: center;
  gap: 0.3rem;
}

.result-action {
  min-width: 2.7rem;
  height: 1.85rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0 0.55rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.35rem;
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  cursor: pointer;
  font-size: 0.75rem;
  font-weight: 550;
  line-height: 1;
  text-decoration: none;
  white-space: nowrap;
}

.result-action:hover {
  border-color: hsl(var(--border-hover));
  background: hsl(var(--muted));
}

.result-open {
  border-color: hsl(var(--primary) / 0.35);
  color: hsl(var(--primary));
}

.result-action.copied {
  color: hsl(var(--success));
  border-color: hsl(var(--success) / 0.35);
  background: hsl(var(--success) / 0.08);
}

.result-action.copy-failed {
  color: hsl(var(--destructive));
}

@container (max-width: 620px) {
  .result-item {
    gap: 0.4rem;
    padding-right: 0.45rem;
    padding-left: 0.55rem;
  }

  .result-date,
  .result-host,
  .result-meta > .meta-separator {
    display: none;
  }

  .result-action {
    min-width: 2.25rem;
    padding: 0 0.4rem;
  }
}

@container (max-width: 430px) {
  .result-copy {
    display: none;
  }

  .result-code {
    max-width: 7.5rem;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

/* 1.2 product-grade result surface */
.empty-state,
.searching-state {
  min-height: 13rem;
  border: 0;
  background: transparent;
}
.initial-mark {
  display: flex;
  align-items: flex-end;
  gap: .24rem;
  height: 1.65rem;
  margin-bottom: .8rem;
}
.initial-mark span {
  width: .34rem;
  border-radius: 999px;
  background: hsl(var(--primary));
  opacity: .9;
}
.initial-mark span:nth-child(1) { height: .7rem; opacity: .35; }
.initial-mark span:nth-child(2) { height: 1.2rem; opacity: .65; }
.initial-mark span:nth-child(3) { height: 1.65rem; }
.initial-tips {
  display: flex;
  align-items: center;
  gap: .9rem;
  margin-top: .85rem;
  color: hsl(var(--muted-foreground));
  font-size: .68rem;
}
.initial-tips kbd {
  padding: .12rem .3rem;
  border: 1px solid hsl(var(--border));
  border-bottom-width: 2px;
  border-radius: .28rem;
  background: hsl(var(--card));
  font: inherit;
}
.results-container {
  border-color: hsl(var(--border));
  border-radius: .65rem;
  box-shadow: 0 1px 2px rgb(15 23 42 / .03), 0 10px 30px rgb(15 23 42 / .035);
}
.tabs {
  min-height: 2.65rem;
  padding: 0 .55rem;
  background: hsl(var(--muted) / .38);
}
.tab-button {
  display: inline-flex;
  align-items: center;
  gap: .35rem;
  padding: .65rem .65rem .58rem;
  color: hsl(var(--muted-foreground));
}
.tab-button small {
  min-width: 1.25rem;
  padding: .08rem .28rem;
  border-radius: 999px;
  background: hsl(var(--border) / .65);
  color: inherit;
  font-size: .64rem;
  line-height: 1.2;
  text-align: center;
}
.tab-button.active small { background: hsl(var(--primary) / .1); }
.result-item {
  grid-template-columns: 1.65rem minmax(0, 1fr) auto;
  min-height: 3.65rem;
  gap: .65rem;
  padding: .48rem .75rem;
}
.result-rank {
  align-self: start;
  padding-top: .15rem;
  color: hsl(var(--muted-foreground) / .58);
  font-size: .7rem;
  font-variant-numeric: tabular-nums;
  text-align: right;
}
.result-title { font-size: .86rem; font-weight: 620; }
.result-meta { margin-top: .22rem; font-size: .69rem; }
.result-source {
  padding: .08rem .3rem;
  border-radius: .26rem;
  background: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
}
.result-action {
  height: 1.8rem;
  min-width: 2.8rem;
  border-radius: .38rem;
  font-size: .72rem;
  font-weight: 620;
}
.result-open {
  color: hsl(var(--primary-foreground));
  border-color: hsl(var(--primary));
  background: hsl(var(--primary));
}
.result-open:hover { background: hsl(var(--primary) / .9); }

@container (max-width: 620px) {
  .result-item { grid-template-columns: minmax(0, 1fr) auto; padding-inline: .6rem; }
  .result-rank { display: none; }
  .tabs { min-height: 2.4rem; padding-inline: .3rem; }
  .tab-button { padding: .55rem .55rem .48rem; }
}
</style>
