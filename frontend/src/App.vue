<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed, nextTick, watch } from 'vue';
import { search, logout, getHealth, verifyToken, type SearchParams, type HealthStatus } from '@/api';
import type { SearchResponse, MergedResults, ExportField, ExportSettings } from '@/types';
import SearchForm from '@/components/SearchForm.vue';
import ResultTabs from '@/components/ResultTabs.vue';
import SearchStats from '@/components/SearchStats.vue';
import SearchConfig from '@/components/SearchConfig.vue';
import LoginDialog from '@/components/LoginDialog.vue';
import ExportResultsModal from '@/components/ExportResultsModal.vue';
import { getDiskTypeName } from '@/utils/diskTypes';
import { loadLocal, saveLocal } from '@/utils/storage';
import { appPath } from '@/utils/appPath';

// 后端健康状态缓存（应用启动时获取一次）
const backendHealth = ref<HealthStatus | null>(null);

// 搜索状态
const loading = ref(false);
const searchResults = reactive<{
  total: number;
  mergedResults: MergedResults;
}>({
  total: 0,
  mergedResults: {}
});

// 搜索时间
const searchTime = ref<number | undefined>(undefined);

// 后台更新状态
const isUpdating = ref(false);
const updateCount = ref(0);
const updateTimer = ref<number | null>(null);
const secondSearchTimeout = ref<number | null>(null);
const thirdSearchTimeout = ref<number | null>(null);
const fourthSearchTimeout = ref<number | null>(null);
const lastSearchParams = ref<SearchParams | null>(null);
const showExportModal = ref(false);
const navHeaderRef = ref<HTMLElement | null>(null);
const mainContentRef = ref<HTMLElement | null>(null);
const searchResultsBlockRef = ref<HTMLElement | null>(null);
const mobileSearchResultsHeight = ref<string>('');
const exportSettings = ref<ExportSettings>({
  format: 'json',
  fields: ['title', 'source', 'datetime'],
  prettyJson: true,
  includeFieldLabels: true,
  selectedDiskTypes: [],
  allDiskTypesSelected: true
});

const EXPORT_SETTINGS_STORAGE_KEY = 'export_settings';

// 是否已经执行过搜索
const hasSearched = ref(false);
// 是否正在进行后台搜索（包括初始搜索和后续更新）
const isActivelySearching = ref(false);
const hasExportableResults = computed(() => {
  return Object.values(searchResults.mergedResults || {}).some(items => Array.isArray(items) && items.length > 0);
});
const exportableDiskTypes = computed(() => {
  return Object.keys(searchResults.mergedResults || {}).filter(key => {
    const items = searchResults.mergedResults[key];
    return Array.isArray(items) && items.length > 0;
  });
});

// 强制刷新逻辑
let forceRefreshPending = false;

// 当前页面状态
const currentPage = ref<'search' | 'status'>('search');

const showLogin = ref(false);
const isAuthenticated = ref(false);
const currentUsername = ref('');
const browserUrl = appPath('/');
const isEmbedded = (() => {
  try {
    return window.self !== window.top;
  } catch {
    return true;
  }
})();

const switchToStatus = () => {
  currentPage.value = 'status';
};

// 初始化后端健康状态（应用启动时调用一次）
const initBackendHealth = async () => {
  try {
    backendHealth.value = await getHealth();
  } catch (err) {
    console.error('获取后端健康状态失败:', err);
    backendHealth.value = null;
  }
};

// 检查配置（优先使用用户设置，否则使用后端默认缓存）
const checkConfig = () => {
  try {
    const savedPlugins = loadLocal('plugins');
    
    // 如果用户已手动设置，使用用户设置
    if (savedPlugins !== null) {
      return {
        channels: [],
        plugins: savedPlugins ? JSON.parse(savedPlugins) : []
      };
    }
    
    // 如果用户未设置，使用缓存的后端配置
    if (backendHealth.value) {
      return {
        channels: [],
        plugins: backendHealth.value.plugins || []
      };
    }
    
    return { channels: [], plugins: [] };
  } catch (err) {
    console.error('检查配置失败:', err);
    return { channels: [], plugins: [] };
  }
};

// 处理搜索
const handleSearch = async (params: SearchParams) => {
  // 停止之前的更新
  stopUpdate();

  // 先保存用户输入的原始参数，不带 refresh
  lastSearchParams.value = { ...params };

  // 强制刷新: 只影响本次请求参数
  let innerParams = { ...params };
  if (forceRefreshPending) {
    innerParams.refresh = true;
    forceRefreshPending = false;
  }

  // 标记状态
  hasSearched.value = true;
  isActivelySearching.value = true;
  loading.value = true;

  // 清空之前的搜索结果
  searchResults.total = 0;
  searchResults.mergedResults = {};
  searchTime.value = undefined;

  const startTime = Date.now();

  // 配置
  const config = checkConfig();
  const hasChannels = config.channels.length > 0;
  const hasPlugins = config.plugins.length > 0;

  try {
    // 只用 innerParams，确保 refresh 只传一次
    const userParams: SearchParams = { ...innerParams };
    
    // 如果同时启用了TG和插件，立即发起后台预热搜索（忽略结果）
    if (hasChannels && hasPlugins) {
      const preloadParams: SearchParams = {
        ...lastSearchParams.value,
        src: 'plugin' // 后台预热搜索也只用插件
      };
      
      // 后台预热搜索，仅用于触发后端插件异步缓存，不处理结果
      search(preloadParams)
        .catch(error => {
          console.warn('后台预热搜索失败（不影响主搜索）:', error);
        });
    }
    
    // 先发起第一次搜索请求（显示结果）
    search(userParams)
      .then(firstResponse => {
        
        if (firstResponse && firstResponse.total !== undefined) {
          // 使用第一次搜索结果进行显示
          updateSearchResults(firstResponse);
          searchTime.value = Date.now() - startTime;
          // 第一次搜索完成后，关闭加载状态
          loading.value = false;
          
          // 根据配置决定是否需要后续搜索：
          // 1. 同时启用tg和plugin：需要第二次、第三次搜索（src=all）
          // 2. 只启用tg：不需要后续搜索
          // 3. 只启用plugin：需要第二次、第三次搜索（src=plugin）
          if (hasPlugins) {
            // 只要启用了插件，就需要后续搜索（插件是异步的）
            // 记录第一次搜索完成时间
            const firstSearchCompleteTime = Date.now();
            
            // 开始第二次搜索
            startSecondAllSearch(firstSearchCompleteTime);
          } else {
            // 只有TG或都没有，不需要后续搜索，标记搜索完成
            isActivelySearching.value = false;
          }
        } else {
          console.error('第一次搜索结果格式不正确:', firstResponse);
          loading.value = false;
          isActivelySearching.value = false;
        }
      })
      .catch(error => {
        console.error('第一次搜索出错:', error);
        loading.value = false;
        isActivelySearching.value = false;
      });
    
    // 设置一个超时，确保即使搜索很慢，UI也不会一直处于加载状态
    setTimeout(() => {
      if (loading.value) {
        loading.value = false;
      }
    }, 5000); // 5秒后如果还在加载，则关闭加载状态
    
  } catch (error) {
    console.error('搜索初始化出错:', error);
    loading.value = false;
    isActivelySearching.value = false;
  }
};

// 搜索完成处理
const handleSearchComplete = () => {
  // 只处理UI相关的状态，不影响搜索流程
};

// 应用关键词过滤（后端filter参数已经处理，这里保留作为备用）
const applyKeywordFilter = (results: any, filterStr: string) => {
  if (!results || !filterStr.trim()) return results;
  
  try {
    const filter = JSON.parse(filterStr);
    const includeKeywords = (filter.include || []).map((k: string) => k.toLowerCase());
    const excludeKeywords = (filter.exclude || []).map((k: string) => k.toLowerCase());
    
    if (includeKeywords.length === 0 && excludeKeywords.length === 0) {
      return results;
    }
    
    const filteredResults: any = {};
    
    // 遍历每个网盘类型的结果
    Object.keys(results).forEach(diskType => {
      const diskResults = results[diskType];
      if (!Array.isArray(diskResults)) return;
      
      // 过滤每个结果项
      const filtered = diskResults.filter((item: any) => {
        const note = (item.note || '').toLowerCase();
        const source = (item.source || '').toLowerCase();
        const searchText = `${note} ${source}`;
        
        // 包含检查 (OR关系)：如果有include，必须至少包含一个
        if (includeKeywords.length > 0) {
          const hasInclude = includeKeywords.some(keyword => searchText.includes(keyword));
          if (!hasInclude) return false;
        }
        
        // 排除检查 (OR关系)：如果有exclude，包含任意一个就排除
        if (excludeKeywords.length > 0) {
          const hasExclude = excludeKeywords.some(keyword => searchText.includes(keyword));
          if (hasExclude) return false;
        }
        
        return true;
      });
      
      // 只保留有结果的网盘类型
      if (filtered.length > 0) {
        filteredResults[diskType] = filtered;
      }
    });
    
    return filteredResults;
  } catch (error) {
    console.error('过滤参数解析失败:', error);
    return results;
  }
};

// 更新搜索结果
const updateSearchResults = (response: SearchResponse) => {
  if (!response) return;
  
  searchResults.total = response.total || 0;
  
  if (response.merged_by_type) {
    let results = { ...response.merged_by_type };
    
    // 注意：后端已经处理了filter参数，这里不再需要前端过滤
    // 保留以下代码作为备用，但不执行
    // if (lastSearchParams.value && (lastSearchParams.value as any).filter) {
    //   results = applyKeywordFilter(results, (lastSearchParams.value as any).filter);
    // }
    
    searchResults.mergedResults = results;
  } else {
    console.warn('搜索结果中没有merged_by_type字段');
    searchResults.mergedResults = {};
  }
};

const loadExportSettings = () => {
  try {
    const saved = loadLocal(EXPORT_SETTINGS_STORAGE_KEY);
    if (!saved) return;

    const parsed = JSON.parse(saved);
    const allowedFields: ExportField[] = ['sequence', 'title', 'source', 'datetime'];
    const fields = Array.isArray(parsed.fields)
      ? parsed.fields.filter((field: unknown): field is ExportField => allowedFields.includes(field as ExportField))
      : exportSettings.value.fields;

    exportSettings.value = {
      format: parsed.format === 'txt' ? 'txt' : 'json',
      fields,
      prettyJson: parsed.prettyJson !== false,
      includeFieldLabels: parsed.includeFieldLabels !== false,
      selectedDiskTypes: Array.isArray(parsed.selectedDiskTypes) ? parsed.selectedDiskTypes : [],
      allDiskTypesSelected: parsed.allDiskTypesSelected !== false
    };
  } catch (error) {
    console.error('读取导出设置失败:', error);
  }
};

const persistExportSettings = () => {
  try {
    saveLocal(EXPORT_SETTINGS_STORAGE_KEY, JSON.stringify(exportSettings.value));
  } catch (error) {
    console.error('保存导出设置失败:', error);
  }
};

const updateExportSettings = (value: ExportSettings) => {
  exportSettings.value = {
    ...value,
    fields: [...value.fields]
  };
  persistExportSettings();
};

const openExportModal = () => {
  if (!hasExportableResults.value) return;
  showExportModal.value = true;
};

const closeExportModal = () => {
  showExportModal.value = false;
};

type ExportRow = Record<ExportField, string>;
type FixedExportFields = 'url' | 'password';
type FullExportRow = ExportRow & Record<FixedExportFields, string> & { diskTypeKey: string; diskType: string };
type ExportLinkBuildResult = {
  url: string;
  passwordIncluded: boolean;
};

const sanitizeFileNamePart = (value: string) => {
  return value
    .trim()
    .replace(/[\\/:*?"<>|]+/g, '-')
    .replace(/\s+/g, '-')
    .slice(0, 48);
};

const normalizePasswordText = (value: string) => {
  return value.trim().toLowerCase();
};

const isPasswordEmbeddedInUrl = (url: string, password: string) => {
  if (!url || !password) return false;

  const normalizedPassword = normalizePasswordText(password);
  if (!normalizedPassword) return false;

  const candidates = new Set<string>([url]);

  try {
    candidates.add(decodeURIComponent(url));
  } catch {}

  try {
    candidates.add(decodeURI(url));
  } catch {}

  return Array.from(candidates).some(candidate => candidate.toLowerCase().includes(normalizedPassword));
};

const buildUrlWithQueryParam = (url: string, key: string, password: string) => {
  try {
    const parsed = new URL(url);
    parsed.searchParams.set(key, password);
    return parsed.toString();
  } catch {
    const connector = url.includes('?') ? '&' : '?';
    return `${url}${connector}${key}=${encodeURIComponent(password)}`;
  }
};

const buildIntegratedExportUrl = (diskTypeKey: string, url: string, password: string): ExportLinkBuildResult => {
  if (!url || !password) {
    return { url, passwordIncluded: !password };
  }

  if (isPasswordEmbeddedInUrl(url, password)) {
    return { url, passwordIncluded: true };
  }

  const ruleMap: Record<string, string | null> = {
    baidu: 'pwd',
    tianyi: 'pwd',
    mobile: 'pwd',
    '123': 'pwd',
    aliyun: 'password',
    quark: 'pwd',
    uc: 'pwd',
    xunlei: 'pwd',
    pikpak: 'pwd'
  };

  const paramName = ruleMap[diskTypeKey];
  if (paramName) {
    return {
      url: buildUrlWithQueryParam(url, paramName, password),
      passwordIncluded: true
    };
  }

  return {
    url,
    passwordIncluded: false
  };
};

const buildCombinedLinkLine = (row: FullExportRow) => {
  const integrated = buildIntegratedExportUrl(row.diskTypeKey, row.url, row.password);

  if (integrated.passwordIncluded || !row.password) {
    return integrated.url;
  }

  return `${integrated.url} 提取码:${row.password}`;
};

const buildExportRows = () => {
  const rows: FullExportRow[] = [];
  const selectedSet = new Set((exportSettings.value.selectedDiskTypes || []).filter(Boolean));

  Object.entries(searchResults.mergedResults || {}).forEach(([diskTypeKey, items]) => {
    if (!Array.isArray(items)) return;
    if (!exportSettings.value.allDiskTypesSelected && !selectedSet.has(diskTypeKey)) return;

    items.forEach((item, index) => {
      const row: FullExportRow = {
        diskTypeKey,
        diskType: getDiskTypeName(diskTypeKey),
        sequence: String(index + 1),
        title: item.note || '',
        url: item.url || '',
        password: item.password || '',
        source: item.source || '',
        datetime: item.datetime || ''
      };

      rows.push(row);
    });
  });

  return rows;
};

const buildExportRecord = (row: FullExportRow, selectedFields: ExportField[]) => {
  const integrated = buildIntegratedExportUrl(row.diskTypeKey, row.url, row.password);
  const record = selectedFields.reduce<Record<string, string>>((record, field) => {
    record[field] = row[field] || '';
    return record;
  }, {
    url: integrated.url
  });

  if (row.password && !integrated.passwordIncluded) {
    record.password = row.password;
  }

  return record;
};

const buildTxtValueLines = (row: FullExportRow, fields: ExportField[]) => {
  const values: string[] = [];

  fields.forEach(field => {
    const value = row[field] || '';
    if (value) {
      values.push(value);
    }
  });

  values.push(buildCombinedLinkLine(row));

  return values.join('\n');
};

const buildJsonExportContent = (rows: FullExportRow[]) => {
  const selectedFields = exportSettings.value.fields;
  const groupedRows = rows.reduce<Record<string, FullExportRow[]>>((acc, row) => {
    if (!acc[row.diskType]) acc[row.diskType] = [];
    acc[row.diskType].push(row);
    return acc;
  }, {});

  const payload = Object.entries(groupedRows).reduce<Record<string, Array<Record<string, string>>>>((acc, [diskType, groupRows]) => {
    acc[diskType] = groupRows.map(row => buildExportRecord(row, selectedFields));
    return acc;
  }, {});

  const meta = {
    keyword: lastSearchParams.value?.kw || '',
    total: searchResults.total,
    exportedAt: new Date().toISOString(),
    fields: [...selectedFields, 'url', 'password'],
    groupedByDiskType: true,
    diskTypes: exportSettings.value.allDiskTypesSelected
      ? ['all']
      : exportSettings.value.selectedDiskTypes
  };

  return JSON.stringify(
    {
      meta,
      results: payload,
      flatResults: rows.map(row => buildExportRecord(row, selectedFields))
    },
    null,
    exportSettings.value.prettyJson ? 2 : 0
  );
};

const fieldLabelMap: Record<ExportField | FixedExportFields, string> = {
  sequence: '序号',
  title: '标题',
  url: '链接',
  password: '提取码',
  source: '来源',
  datetime: '时间'
};

const buildTxtRow = (row: FullExportRow, fields: ExportField[]) => {
  if (!exportSettings.value.includeFieldLabels) {
    return buildTxtValueLines(row, fields);
  }

  const lines = fields.map(field => `${fieldLabelMap[field]}: ${row[field] || '无'}`);
  lines.push(`${fieldLabelMap.url}: ${buildCombinedLinkLine(row)}`);

  return lines.join('\n');
};

const buildTxtExportContent = (rows: FullExportRow[]) => {
  const blocks: string[] = [];
  const fields = exportSettings.value.fields;

  const grouped = rows.reduce<Record<string, FullExportRow[]>>((acc, row) => {
    if (!acc[row.diskType]) acc[row.diskType] = [];
    acc[row.diskType].push(row);
    return acc;
  }, {});

  Object.entries(grouped).forEach(([diskType, groupRows]) => {
    blocks.push(`${diskType} (${groupRows.length})`);
    blocks.push(groupRows.map((row) => buildTxtRow(row, fields)).join('\n\n'));
  });

  return blocks.filter(Boolean).join('\n\n');
};

const downloadContent = (content: string, fileName: string, mimeType: string) => {
  const blob = new Blob([content], { type: `${mimeType};charset=utf-8` });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = fileName;
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
  URL.revokeObjectURL(url);
};

const handleExportConfirm = () => {
  if (!hasExportableResults.value) return;

  persistExportSettings();

  const rows = buildExportRows();
  const keyword = sanitizeFileNamePart(lastSearchParams.value?.kw || 'results');
  const timestamp = new Date().toISOString().replace(/[:.]/g, '-');

  if (exportSettings.value.format === 'json') {
    const content = buildJsonExportContent(rows);
    downloadContent(content, `xiaobaisoupan-${keyword}-${timestamp}.json`, 'application/json');
  } else {
    const content = buildTxtExportContent(rows);
    downloadContent(content, `xiaobaisoupan-${keyword}-${timestamp}.txt`, 'text/plain');
  }

  closeExportModal();
};

const updateMobileSearchResultsHeight = () => {
  if (typeof window === 'undefined' || window.innerWidth > 768) {
    mobileSearchResultsHeight.value = '';
    return;
  }

  const resultsBlock = searchResultsBlockRef.value;

  if (!resultsBlock) {
    mobileSearchResultsHeight.value = '';
    return;
  }

  const resultsTop = resultsBlock.getBoundingClientRect().top;
  const availableHeight = Math.floor(window.innerHeight - resultsTop - 8);

  mobileSearchResultsHeight.value = availableHeight > 0 ? `${availableHeight}px` : '';
};

const syncMobileSearchLayout = () => {
  nextTick(() => {
    updateMobileSearchResultsHeight();
  });
};

watch(
  () => [currentPage.value, hasSearched.value, loading.value, searchResults.total, isActivelySearching.value],
  () => {
    syncMobileSearchLayout();
  },
  { deep: true }
);

// 插件-only：后续搜索固定只走 plugin
const calculateSrcForFullSearch = (): 'plugin' => {
  return 'plugin';
};

// 开始第二次搜索
const startSecondAllSearch = (firstSearchCompleteTime: number) => {
  if (!lastSearchParams.value) return;
  
  isUpdating.value = true;
  isActivelySearching.value = true;
  updateCount.value = 1;
  
  // 第二次搜索：根据完整配置设置src
  const src = calculateSrcForFullSearch();
  const userParams: SearchParams = { 
    ...lastSearchParams.value,
    src: src  // 使用完整配置的src
  };
  
  // 计算需要等待的时间，确保与第一次搜索至少间隔2秒
  const currentTime = Date.now();
  const timeElapsedSinceFirstSearch = currentTime - firstSearchCompleteTime;
  const delayForSecondSearch = Math.max(0, 2000 - timeElapsedSinceFirstSearch);
  
  // 执行第二次搜索
  const executeSecondSearch = async () => {
    if (!lastSearchParams.value) {
      stopUpdate();
      return;
    }
    
    try {
      const response = await search(userParams);
      
      // 更新结果
      if (response && response.total >= searchResults.total) {
        updateSearchResults(response);
      }
      
      // 记录第二次搜索完成时间
      const secondSearchCompleteTime = Date.now();
      
      // 开始第三次搜索
      startThirdAllSearch(secondSearchCompleteTime);
    } catch (error) {
      console.error('第二次搜索出错:', error);
      stopUpdate();
    }
  };
  
  // 设置定时器，在适当的时间执行第二次搜索
  secondSearchTimeout.value = window.setTimeout(executeSecondSearch, delayForSecondSearch);
};

// 开始第三次搜索
const startThirdAllSearch = (secondSearchCompleteTime: number) => {
  if (!lastSearchParams.value) return;
  
  updateCount.value = 2;
  
  // 第三次搜索：根据完整配置设置src
  const src = calculateSrcForFullSearch();
  const userParams: SearchParams = { 
    ...lastSearchParams.value,
    src: src  // 使用完整配置的src
  };
  
  // 计算需要等待的时间，确保与第二次搜索至少间隔3秒
  const currentTime = Date.now();
  const timeElapsedSinceSecondSearch = currentTime - secondSearchCompleteTime;
  const delayForThirdSearch = Math.max(0, 3000 - timeElapsedSinceSecondSearch);
  
  // 执行第三次搜索
  const executeThirdSearch = async () => {
    if (!lastSearchParams.value) {
      stopUpdate();
      return;
    }
    
    try {
      const response = await search(userParams);
      
      // 更新结果
      if (response && response.total >= searchResults.total) {
        updateSearchResults(response);
      }
      
      // 记录第三次搜索完成时间
      const thirdSearchCompleteTime = Date.now();
      
      // 检查是否需要第四次搜索（只有启用插件时才需要）
      const config = checkConfig();
      const hasPlugins = config.plugins.length > 0;
      
      if (hasPlugins) {
        // 开始第四次搜索
        startFourthAllSearch(thirdSearchCompleteTime);
      } else {
        // 没有插件，完成所有搜索
        stopUpdate();
      }
    } catch (error) {
      console.error('第三次搜索出错:', error);
      stopUpdate();
    }
  };
  
  // 设置定时器，在适当的时间执行第三次搜索
  thirdSearchTimeout.value = window.setTimeout(executeThirdSearch, delayForThirdSearch);
};

// 开始第四次搜索
const startFourthAllSearch = (thirdSearchCompleteTime: number) => {
  if (!lastSearchParams.value) return;
  
  updateCount.value = 3;
  
  // 第四次搜索：根据完整配置设置src
  const src = calculateSrcForFullSearch();
  const userParams: SearchParams = { 
    ...lastSearchParams.value,
    src: src  // 使用完整配置的src
  };
  
  // 计算需要等待的时间，确保与第三次搜索至少间隔3秒
  const currentTime = Date.now();
  const timeElapsedSinceThirdSearch = currentTime - thirdSearchCompleteTime;
  const delayForFourthSearch = Math.max(0, 3000 - timeElapsedSinceThirdSearch);
  
  // 执行第四次搜索
  const executeFourthSearch = async () => {
    if (!lastSearchParams.value) {
      stopUpdate();
      return;
    }
    
    try {
      const response = await search(userParams);
      
      // 更新结果
      if (response && response.total >= searchResults.total) {
        updateSearchResults(response);
      }
    } catch (error) {
      console.error('第四次搜索出错:', error);
    } finally {
      // 完成所有搜索，停止更新
      stopUpdate();
    }
  };
  
  // 设置定时器，在适当的时间执行第四次搜索
  fourthSearchTimeout.value = window.setTimeout(executeFourthSearch, delayForFourthSearch);
};

// 停止后台更新
const stopUpdate = () => {
  // 清除所有定时器
  if (updateTimer.value) {
    clearInterval(updateTimer.value);
    updateTimer.value = null;
  }
  
  if (secondSearchTimeout.value) {
    clearTimeout(secondSearchTimeout.value);
    secondSearchTimeout.value = null;
  }
  
  if (thirdSearchTimeout.value) {
    clearTimeout(thirdSearchTimeout.value);
    thirdSearchTimeout.value = null;
  }
  
  if (fourthSearchTimeout.value) {
    clearTimeout(fourthSearchTimeout.value);
    fourthSearchTimeout.value = null;
  }
  
  // 标记搜索已结束
  isUpdating.value = false;
  isActivelySearching.value = false;
};

// 切换到搜索页面（保持搜索结果）
const switchToSearch = () => {
  currentPage.value = 'search';
};

// 重置到初始页面（清空搜索结果，仅在必要时使用）
const resetToInitial = () => {
  // 停止之前的更新
  stopUpdate();
  
  // 切换到搜索页面
  currentPage.value = 'search';
  
  // 重置所有状态
  hasSearched.value = false;
  isActivelySearching.value = false;
  loading.value = false;
  searchResults.total = 0;
  searchResults.mergedResults = {};
  searchTime.value = undefined;
  isUpdating.value = false;
  updateCount.value = 0;
};

// 检查认证状态（使用缓存的健康状态，避免重复调用API）
const checkAuth = async () => {
  try {
    // 使用缓存的健康状态，避免重复调用 /health
    const authEnabled = backendHealth.value?.auth_enabled || false;
    const token = localStorage.getItem('auth_token');
    
    if (!authEnabled) {
      // 认证未启用，不显示任何登录相关信息
      isAuthenticated.value = false;
      showLogin.value = false;
      return;
    }
    
    if (!token) {
      // 需要认证但没有token
      showLogin.value = true;
      isAuthenticated.value = false;
      return;
    }
    
    // 验证token是否有效
    const valid = await verifyToken();
    if (valid) {
      isAuthenticated.value = true;
      currentUsername.value = localStorage.getItem('auth_username') || '';
    } else {
      showLogin.value = true;
      isAuthenticated.value = false;
    }
  } catch (error) {
    console.error('检查认证状态失败:', error);
    // 出错时默认显示登录窗口
    showLogin.value = true;
    isAuthenticated.value = false;
  }
};

// 监听401事件
const handleAuthRequired = () => {
  showLogin.value = true;
};

// 登录成功处理
const handleLoginSuccess = () => {
  window.location.reload();
};

// 退出登录
const handleLogout = async () => {
  if (confirm('确定要退出登录吗？')) {
    await logout();
    window.location.reload();
  }
};

const handleForceRefresh = () => {
  if (loading.value) return;
  forceRefreshPending = true;
  // 复用handleSearch最近一次参数
  if (lastSearchParams.value) {
    handleSearch({ ...lastSearchParams.value });
  }
};

// 组件加载时初始化
onMounted(async () => {
  // 首先初始化后端健康状态（只调用一次）
  await initBackendHealth();
  loadExportSettings();
  syncMobileSearchLayout();
  
  // 然后初始化其他状态
  checkAuth();
  window.addEventListener('auth:required', handleAuthRequired);
  window.addEventListener('resize', syncMobileSearchLayout);
});

onUnmounted(() => {
  // 确保在组件卸载时清理所有定时器和事件监听
  stopUpdate();
  window.removeEventListener('auth:required', handleAuthRequired);
  window.removeEventListener('resize', syncMobileSearchLayout);
});
</script>

<template>
  <div
    class="app-shell min-h-screen bg-background text-foreground flex flex-col"
    :class="isEmbedded ? 'embedded' : 'standalone'"
  >
    <!-- 登录对话框 -->
    <LoginDialog 
      v-model:visible="showLogin" 
      @success="handleLoginSuccess"
    />
    
    <!-- 背景装饰 -->
    <div class="bg-decorative"></div>
    
    <!-- 导航栏 -->
    <nav ref="navHeaderRef" class="nav-header">
      <div class="nav-inner">
        <button type="button" class="brand" @click="switchToSearch" aria-label="返回搜索">
          <span class="brand-mark">
            <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="m15.5 15.5 4 4" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" />
              <circle cx="10.5" cy="10.5" r="5.75" stroke="currentColor" stroke-width="2.2" />
              <path d="M8.25 10.5h4.5M10.5 8.25v4.5" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" />
            </svg>
          </span>
          <span class="brand-copy">
            <strong class="brand-name">小白搜盘</strong>
          </span>
        </button>
        
        <!-- 导航菜单 -->
        <nav class="nav-menu" aria-label="主导航">
          <button 
            @click="switchToSearch"
            class="nav-button"
            :class="{ 'active': currentPage === 'search' }"
            title="搜索"
          >
            <span class="nav-icon">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"></path>
              </svg>
            </span>
            <span class="nav-text">搜索</span>
          </button>
          <button 
            @click="switchToStatus"
            class="nav-button"
            :class="{ 'active': currentPage === 'status' }"
            title="来源"
          >
            <span class="nav-icon">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"></path>
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"></path>
              </svg>
            </span>
            <span class="nav-text">设置</span>
          </button>
          <a
            v-if="isEmbedded"
            :href="browserUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="nav-button nav-link"
            title="在浏览器中打开"
            aria-label="在浏览器中打开"
          >
            <span class="nav-icon">
              <svg fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14 5h5m0 0v5m0-5-8 8"></path>
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 13v5a1 1 0 01-1 1H6a1 1 0 01-1-1V6a1 1 0 011-1h5"></path>
              </svg>
            </span>
            <span class="nav-text">浏览器打开</span>
          </a>
          <button 
            v-if="isAuthenticated"
            @click="handleLogout"
            class="nav-button logout-button"
            :title="'退出登录 (当前用户: ' + currentUsername + ')'"
          >
            <span class="nav-icon">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"></path>
              </svg>
            </span>
            <span class="nav-text">退出</span>
          </button>
        </nav>
      </div>
    </nav>
    
    <!-- 主要内容区域 -->
    <main
      ref="mainContentRef"
      class="main-content flex-1"
      :class="{ 'search-main': currentPage === 'search', 'has-results': hasSearched }"
    >
      <!-- 搜索页面 -->
      <div v-if="currentPage === 'search'" class="search-page" :class="{ 'has-searched': hasSearched }">
        <!-- 搜索表单 -->
        <div class="search-toolbar">
          <div class="search-form-block" :class="{ 'is-compact': hasSearched }">
            <SearchForm
              :backend-health="backendHealth"
              @search="handleSearch"
              @search-complete="handleSearchComplete"
            />
          </div>

          <div v-if="isEmbedded" class="embedded-actions">
            <button
              type="button"
              class="compact-action"
              title="来源设置"
              aria-label="来源设置"
              @click="switchToStatus"
            >
              <svg fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15.5a3.5 3.5 0 100-7 3.5 3.5 0 000 7z"></path>
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19.4 15a1.7 1.7 0 00.34 1.88l.06.06a2 2 0 01-2.83 2.83l-.06-.06A1.7 1.7 0 0015 19.4a1.7 1.7 0 00-1 .6 1.7 1.7 0 00-.4 1.1V21a2 2 0 01-4 0v-.09A1.7 1.7 0 008.5 19.4a1.7 1.7 0 00-1.88.34l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.7 1.7 0 004.6 15a1.7 1.7 0 00-.6-1 1.7 1.7 0 00-1.1-.4H3a2 2 0 010-4h.09A1.7 1.7 0 004.6 8.5a1.7 1.7 0 00-.34-1.88l-.06-.06a2 2 0 012.83-2.83l.06.06A1.7 1.7 0 009 4.6a1.7 1.7 0 001-.6 1.7 1.7 0 00.4-1.1V3a2 2 0 014 0v.09A1.7 1.7 0 0015.5 4.6a1.7 1.7 0 001.88-.34l.06-.06a2 2 0 012.83 2.83l-.06.06A1.7 1.7 0 0019.4 9c.13.38.34.73.6 1 .3.29.69.45 1.1.45H21a2 2 0 010 4h-.09A1.7 1.7 0 0019.4 15z"></path>
              </svg>
            </button>
            <a
              :href="browserUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="compact-action"
              title="在浏览器中打开"
              aria-label="在浏览器中打开"
            >
              <svg fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14 5h5m0 0v5m0-5-8 8"></path>
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 13v5a1 1 0 01-1 1H6a1 1 0 01-1-1V6a1 1 0 011-1h5"></path>
              </svg>
            </a>
          </div>
        </div>
        
        <!-- 搜索统计 -->
        <div v-if="hasSearched || loading" class="search-stats-block">
          <SearchStats 
            :total="searchResults.total || 0" 
            :mergedResults="searchResults.mergedResults || {}" 
            :loading="loading"
            :searchTime="searchTime"
            :isUpdating="isUpdating"
            :updateCount="updateCount"
            :canExport="hasExportableResults"
            @export-results="openExportModal"
            @force-refresh="handleForceRefresh"
          />
        </div>
        
        <!-- 加载状态 -->
        <div v-if="loading" class="search-loading-block card p-6">
          <div class="space-y-3">
            <div class="h-4 bg-muted rounded animate-pulse"></div>
            <div class="h-4 bg-muted rounded animate-pulse w-3/4"></div>
            <div class="h-4 bg-muted rounded animate-pulse w-1/2"></div>
            <div class="h-4 bg-muted rounded animate-pulse w-2/3"></div>
            <div class="h-4 bg-muted rounded animate-pulse"></div>
          </div>
        </div>
        
        <!-- 搜索结果 -->
        <div
          v-else
          ref="searchResultsBlockRef"
          class="search-results-block"
          :style="mobileSearchResultsHeight ? {
            height: mobileSearchResultsHeight,
            minHeight: mobileSearchResultsHeight,
            maxHeight: mobileSearchResultsHeight
          } : undefined"
        >
          <ResultTabs 
            :mergedResults="searchResults.mergedResults || {}" 
            :loading="loading"
            :hasSearched="hasSearched"
            :isActivelySearching="isActivelySearching"
          />
        </div>

        <ExportResultsModal
          :visible="showExportModal"
          :total="searchResults.total || 0"
          :settings="exportSettings"
          :available-disk-types="exportableDiskTypes"
          @close="closeExportModal"
          @update:settings="updateExportSettings"
          @confirm="handleExportConfirm"
        />
      </div>
      
      <!-- 配置页面 -->
      <div v-else-if="currentPage === 'status'" class="status-page">
        <div v-if="isEmbedded" class="status-toolbar">
          <button type="button" class="status-back" @click="switchToSearch">
            <svg fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7"></path>
            </svg>
            返回搜索
          </button>
          <span>来源设置</span>
        </div>
        <SearchConfig :backend-health="backendHealth" />
      </div>
    </main>
  </div>
</template>

<style scoped>
.bg-decorative {
  position: fixed;
  inset: 0;
  z-index: -10;
  background:
    radial-gradient(circle at 18% 8%, hsl(var(--primary) / 0.12), transparent 26rem),
    radial-gradient(circle at 82% 18%, hsl(var(--accent-secondary) / 0.12), transparent 24rem);
  opacity: 1;
}

.app-shell {
  isolation: isolate;
}

.nav-inner {
  width: min(100%, 1180px);
  height: 4rem;
  margin: 0 auto;
  padding: 0 1.5rem;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.brand {
  appearance: none;
  display: inline-flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0;
  color: hsl(var(--foreground));
  background: transparent;
  border: 0;
  cursor: pointer;
  text-align: left;
}

.brand-mark {
  width: 2.25rem;
  height: 2.25rem;
  display: grid;
  place-items: center;
  color: white;
  border-radius: 0.75rem;
  background: linear-gradient(135deg, hsl(var(--primary)), hsl(var(--accent-secondary)));
  box-shadow: 0 8px 20px hsl(var(--primary) / 0.25);
}

.brand-mark svg {
  width: 1.35rem;
  height: 1.35rem;
}

.brand-copy {
  display: flex;
  flex-direction: column;
  gap: 0.18rem;
}

.brand-name {
  font-size: 1rem;
  font-weight: 750;
  letter-spacing: 0.04em;
  line-height: 1;
}

.brand-copy small {
  font-size: 0.6875rem;
  color: hsl(var(--muted-foreground));
  line-height: 1;
}

.nav-header {
  position: sticky;
  top: 0;
  z-index: 50;
  background: hsl(var(--background) / 0.82);
  border-bottom: 1px solid hsl(var(--border) / 0.8);
  backdrop-filter: blur(18px) saturate(140%);
}

.nav-menu {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.25rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.75rem;
  background: hsl(var(--card) / 0.76);
}

/* 导航按钮样式 */
.nav-button {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.48rem 0.8rem;
  background: transparent;
  color: hsl(var(--muted-foreground));
  border: 0;
  border-radius: 0.55rem;
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
}

.nav-button:hover {
  background: hsl(var(--muted));
  color: hsl(var(--foreground));
}

.nav-button.active {
  background: hsl(var(--primary) / 0.12);
  color: hsl(var(--primary));
  box-shadow: inset 0 0 0 1px hsl(var(--primary) / 0.16);
}

.logout-button {
  border-color: hsl(0, 84%, 60%);
  color: hsl(0, 84%, 60%);
}

.logout-button:hover {
  background: hsl(0, 84%, 95%);
  border-color: hsl(0, 84%, 60%);
  color: hsl(0, 84%, 50%);
}

@media (prefers-color-scheme: dark) {
  .logout-button:hover {
    background: hsl(0, 84%, 20%);
    color: hsl(0, 84%, 90%);
  }
}



.nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.nav-icon svg {
  width: 1rem;
  height: 1rem;
}

.nav-text {
  white-space: nowrap;
}

.nav-link {
  text-decoration: none;
}

/* 页面切换动画 */
.search-page, .status-page {
  animation: fadeIn 0.3s ease-in-out;
}

.main-content {
  width: min(100%, 1120px);
  margin: 0 auto;
  padding: 2.25rem 1.5rem 2rem;
}

.search-page {
  width: 100%;
}

.search-hero {
  max-width: 720px;
  margin: 1.5rem auto 1.65rem;
  text-align: center;
}

.hero-kicker {
  width: fit-content;
  margin: 0 auto 1rem;
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.34rem 0.72rem;
  color: hsl(var(--primary));
  background: hsl(var(--primary) / 0.08);
  border: 1px solid hsl(var(--primary) / 0.13);
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 650;
}

.hero-status {
  width: 0.42rem;
  height: 0.42rem;
  border-radius: 50%;
  background: #22c55e;
  box-shadow: 0 0 0 4px rgb(34 197 94 / 0.12);
}

.search-hero h2 {
  margin: 0;
  font-size: clamp(2rem, 4vw, 3.1rem);
  line-height: 1.12;
  font-weight: 780;
  letter-spacing: -0.055em;
  color: hsl(var(--foreground));
}

.search-hero p {
  margin: 0.9rem 0 0;
  color: hsl(var(--muted-foreground));
  font-size: 0.95rem;
}

.search-form-block {
  max-width: 760px;
  margin: 0 auto 1.25rem;
  padding: 0.6rem;
  border: 1px solid hsl(var(--border));
  border-radius: 1.15rem;
  background: hsl(var(--card) / 0.94);
  box-shadow: 0 18px 48px hsl(var(--shadow-color) / 0.1), 0 2px 8px hsl(var(--shadow-color) / 0.05);
  transition: max-width 0.25s ease, margin 0.25s ease;
}

.search-form-block.is-compact {
  max-width: none;
  margin-bottom: 0.9rem;
}

.search-stats-block {
  margin-bottom: 0.9rem;
}

.search-results-block {
  min-height: 18rem;
}

.status-page {
  max-width: 900px;
  margin: 0 auto;
}

@keyframes fadeIn {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@media (max-width: 768px) {
  .app-shell {
    height: 100dvh;
    min-height: 100dvh;
    overflow: hidden;
  }

  .main-content {
    flex: 1 1 auto;
    height: calc(100dvh - 4rem);
    min-height: 0;
    box-sizing: border-box;
    padding-top: 1rem;
    padding-bottom: calc(0.5rem + env(safe-area-inset-bottom));
    overflow: hidden;
    overflow-x: hidden;
  }

  .main-content:not(.search-main) {
    overflow-y: auto;
  }

  .search-page {
    display: grid;
    grid-template-rows: auto auto minmax(0, 1fr);
    height: 100%;
    min-height: 0;
    gap: 1rem;
  }

  .search-form-block,
  .search-stats-block,
  .search-loading-block {
    margin-bottom: 0 !important;
    min-height: 0;
  }

  .search-results-block {
    display: flex;
    min-height: 0;
    overflow: hidden;
  }

  .container {
    padding-left: 1rem;
    padding-right: 1rem;
  }

  .nav-inner {
    padding: 0 0.9rem;
  }

  .brand-copy small {
    display: none;
  }

  .brand-mark {
    width: 2rem;
    height: 2rem;
    border-radius: 0.65rem;
  }

  .search-hero {
    margin: 0.75rem auto 1.25rem;
  }

  .search-hero h2 {
    font-size: 2rem;
  }

  .search-hero p {
    font-size: 0.85rem;
    padding: 0 0.5rem;
  }

  .search-form-block {
    padding: 0.42rem;
    border-radius: 0.9rem;
  }
  
  /* 移动端按钮样式 - 只显示图标 */
  .nav-button {
    padding: 0.5rem;
    font-size: 0.8rem;
    min-width: 2.5rem;
    justify-content: center;
  }
  
  /* 移动端隐藏按钮文字 */
  .nav-text {
    display: none;
  }
}

/* fnOS 小窗口：保持工具密度，把空间留给结果 */
.bg-decorative {
  background: hsl(var(--background));
}

.nav-inner {
  width: 100%;
  height: 3rem;
  padding: 0 0.75rem;
}

.brand {
  gap: 0.5rem;
}

.brand-mark {
  width: 1.75rem;
  height: 1.75rem;
  border-radius: 0.4rem;
  background: hsl(var(--primary));
  box-shadow: none;
}

.brand-mark svg {
  width: 1rem;
  height: 1rem;
}

.brand-name {
  font-size: 0.9375rem;
  font-weight: 700;
  letter-spacing: 0;
}

.nav-header {
  background: hsl(var(--card));
  border-bottom-color: hsl(var(--border));
  backdrop-filter: none;
}

.nav-menu {
  gap: 0.125rem;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
}

.nav-button {
  gap: 0.35rem;
  padding: 0.35rem 0.55rem;
  border-radius: 0.35rem;
  font-size: 0.75rem;
}

.nav-button.active {
  background: hsl(var(--primary) / 0.08);
  box-shadow: none;
}

.main-content {
  width: 100%;
  padding: 0.75rem;
}

.search-form-block,
.search-form-block.is-compact {
  width: 100%;
  max-width: none;
  margin: 0 0 0.5rem;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.search-stats-block {
  margin-bottom: 0.5rem;
}

.search-results-block {
  min-height: 10rem;
}

@media (max-width: 768px) {
  .main-content {
    height: calc(100dvh - 3rem);
    padding: 0.5rem;
  }

  .search-page {
    gap: 0.5rem;
  }

  .nav-inner {
    padding: 0 0.5rem;
  }

  .brand-mark {
    width: 1.65rem;
    height: 1.65rem;
    border-radius: 0.35rem;
  }

  .search-form-block {
    padding: 0;
  }
}

/* 同一应用，两种宿主布局 */
.search-toolbar {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
}

.search-toolbar .search-form-block {
  flex: 1 1 auto;
  min-width: 0;
}

.embedded .nav-header {
  display: none;
}

.embedded.app-shell {
  height: 100dvh;
  min-height: 100dvh;
  overflow: hidden;
}

.embedded .main-content {
  flex: 1 1 0;
  height: auto;
  min-height: 0;
  box-sizing: border-box;
  padding: 0.5rem;
  overflow: hidden;
}

.embedded .search-page {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  height: 100%;
  min-height: 0;
  gap: 0.5rem;
}

.embedded .search-page.has-searched {
  grid-template-rows: auto auto minmax(0, 1fr);
}

.embedded .search-form-block,
.embedded .search-form-block.is-compact {
  margin: 0;
}

.embedded-actions {
  display: flex;
  align-items: center;
  gap: 0.3rem;
  flex: 0 0 auto;
}

.compact-action {
  width: 2.5rem;
  height: 2.5rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  color: hsl(var(--muted-foreground));
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: 0.5rem;
  text-decoration: none;
  cursor: pointer;
}

.compact-action:hover {
  color: hsl(var(--primary));
  border-color: hsl(var(--primary) / 0.45);
}

.compact-action svg {
  width: 1rem;
  height: 1rem;
}

.embedded .search-stats-block {
  margin: 0;
}

.embedded .search-results-block {
  display: flex;
  min-height: 0;
  overflow: hidden;
}

.embedded :deep(.results-wrapper),
.embedded :deep(.results-container),
.embedded :deep(.tab-content) {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
}

.embedded :deep(.results-wrapper),
.embedded :deep(.results-container) {
  width: 100%;
  height: 100%;
}

.embedded :deep(.result-list) {
  flex: 1 1 auto;
  max-height: none;
  min-height: 0;
  padding: 0.2rem 0.35rem;
}

/* 飞牛窗口只保留一行摘要，详细链接点标题后查看。 */
.embedded :deep(.tabs) {
  min-height: 2.25rem;
  box-sizing: border-box;
  align-items: center;
}

.embedded :deep(.result-item) {
  min-height: 2.3rem;
  margin: 0;
  padding: 0.42rem 0.55rem;
  border-radius: 0;
  border-bottom: 1px solid hsl(var(--border) / 0.55);
}

.embedded :deep(.result-item:last-child) {
  border-bottom: 0;
}

.embedded :deep(.result-header) {
  align-items: center;
}

.embedded :deep(.result-title) {
  font-size: 0.85rem;
  line-height: 1.35;
}

.embedded :deep(.result-row) {
  display: none;
}

.embedded :deep(.result-meta.desktop-only) {
  margin-left: 0.5rem;
}

.embedded :deep(.result-meta .meta-separator),
.embedded :deep(.result-meta .result-date) {
  display: none;
}

@media (max-width: 768px) {
  .embedded :deep(.result-meta.mobile-only) {
    display: none !important;
  }
}

.embedded :deep(.empty-state) {
  flex: 1 1 auto;
  min-height: 0;
  border: 0;
  background: transparent;
}

.standalone .main-content {
  width: min(100%, 1280px);
  padding: 1.25rem 1.5rem 2rem;
}

.standalone .main-content:not(.has-results) .search-page {
  width: min(100%, 880px);
  margin: clamp(2rem, 9vh, 5.5rem) auto 0;
}

.standalone .main-content:not(.has-results) .search-results-block {
  min-height: 12rem;
}

.standalone .search-form-block,
.standalone .search-form-block.is-compact {
  margin-bottom: 0.75rem;
}

.status-toolbar {
  height: 2.5rem;
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.5rem;
  color: hsl(var(--foreground));
  font-size: 0.875rem;
  font-weight: 600;
}

.status-back {
  height: 2rem;
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0 0.55rem;
  color: hsl(var(--muted-foreground));
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: 0.4rem;
  cursor: pointer;
}

.status-back svg {
  width: 0.9rem;
  height: 0.9rem;
}

.embedded .status-page {
  max-width: none;
  height: 100%;
  overflow-y: auto;
}

@media (max-width: 768px) {
  .standalone .main-content {
    padding: 0.75rem;
  }

  .standalone .main-content:not(.has-results) .search-page {
    margin-top: 1.25rem;
  }
}
</style>
