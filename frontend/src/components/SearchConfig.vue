<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { type HealthStatus } from '@/api';
import type { DetectionSettings } from '@/types';
import { loadDetectionSettings, persistDetectionSettings } from '@/utils/linkDetection';
import { loadLocal, saveLocal } from '@/utils/storage';
import { appPath } from '@/utils/appPath';

const props = defineProps<{
  backendHealth: HealthStatus | null;
}>();

// 仅保留“插件来源”的配置项（去掉非插件来源）
const diskTypes = [
  { id: 'baidu', name: '百度', color: '#2932e1' },
  { id: 'aliyun', name: '阿里', color: '#ff6a00' },
  { id: 'quark', name: '夸克', color: '#1890ff' },
  { id: 'tianyi', name: '天翼', color: '#0066cc' },
  { id: 'uc', name: 'UC', color: '#ff6600' },
  { id: '115', name: '115', color: '#02a7f0' },
  { id: 'xunlei', name: '迅雷', color: '#0090ff' },
  { id: 'mobile', name: '移动', color: '#0080ff' },
  { id: 'pikpak', name: 'PikPak', color: '#ff4785' },
  { id: '123', name: '123', color: '#00b96b' },
  { id: 'magnet', name: '磁力', color: '#722ed1' },
  { id: 'ed2k', name: '电驴', color: '#fa8c16' },
];

const healthData = ref<HealthStatus | null>(null);
const loading = ref(true);
const error = ref<string | null>(null);

type ActiveTab = 'plugins' | 'diskTypes' | 'detection';
const activeTab = ref<ActiveTab>('plugins');

const detectionSettings = ref<DetectionSettings>(loadDetectionSettings());
const selectedPlugins = ref<string[]>([]);
const selectedDiskTypes = ref<string[]>([]);

const pluginPing = ref<
  Record<string, { net?: { state?: string }; use?: { state?: string } }>
>({});
let pingTimer = 0;

const pingNet = (name: string) => pluginPing.value[name]?.net?.state || 'wait';
const pingUse = (name: string) => pluginPing.value[name]?.use?.state || 'wait';

const availablePlugins = computed(() => healthData.value?.plugins || []);

const stats = computed(() => ({
  plugins: selectedPlugins.value.length,
  diskTypes: selectedDiskTypes.value.length,
}));

const initHealth = () => {
  loading.value = true;
  error.value = null;
  if (props.backendHealth) {
    healthData.value = props.backendHealth;
    loading.value = false;
  } else {
    error.value = '获取状态失败';
    loading.value = false;
  }
};

const normalizeDiskTypes = (saved: string[]) => {
  const ids = new Set(diskTypes.map((d) => d.id));
  return saved.filter((x) => ids.has(x));
};

const loadConfig = () => {
  try {
    const savedPlugins = loadLocal('plugins');
    const savedDiskTypes = loadLocal('disk_types');

    if (savedPlugins) {
      selectedPlugins.value = JSON.parse(savedPlugins) || [];
    } else if (healthData.value?.plugins) {
      selectedPlugins.value = [...healthData.value.plugins];
    }

    if (savedDiskTypes) {
      selectedDiskTypes.value = normalizeDiskTypes(JSON.parse(savedDiskTypes) || []);
    } else {
      selectedDiskTypes.value = diskTypes.map((d) => d.id);
    }
  } catch (err) {
    console.error('加载配置失败:', err);
  }
};

const togglePlugin = (plugin: string) => {
  const idx = selectedPlugins.value.indexOf(plugin);
  if (idx >= 0) selectedPlugins.value.splice(idx, 1);
  else selectedPlugins.value.push(plugin);
};

const toggleAllPlugins = () => {
  selectedPlugins.value =
    selectedPlugins.value.length === availablePlugins.value.length
      ? []
      : [...availablePlugins.value];
};

const toggleDiskType = (id: string) => {
  const idx = selectedDiskTypes.value.indexOf(id);
  if (idx >= 0) selectedDiskTypes.value.splice(idx, 1);
  else selectedDiskTypes.value.push(id);
};

const toggleAllDiskTypes = () => {
  selectedDiskTypes.value =
    selectedDiskTypes.value.length === diskTypes.length
      ? []
      : diskTypes.map((d) => d.id);
};

const saveSuccess = ref(false);
const saveTimeout = ref<number | null>(null);

const saveConfig = () => {
  try {
    saveLocal('plugins', JSON.stringify(selectedPlugins.value));
    saveLocal('disk_types', JSON.stringify(selectedDiskTypes.value));
    persistDetectionSettings(detectionSettings.value);

    saveSuccess.value = true;
    if (saveTimeout.value) clearTimeout(saveTimeout.value);
    saveTimeout.value = window.setTimeout(() => (saveSuccess.value = false), 2000);

    window.dispatchEvent(new CustomEvent('config:saved'));
  } catch (err) {
    console.error('保存配置失败:', err);
  }
};

const resetToDefault = () => {
  if (!healthData.value) return;
  if (!confirm('确定要重置为默认配置吗？')) return;
  selectedPlugins.value = [...healthData.value.plugins];
  selectedDiskTypes.value = diskTypes.map((d) => d.id);
  detectionSettings.value = { enabled: false };
  saveConfig();
};

const loadPluginPing = async (refresh = false) => {
  try {
    const res = await fetch(appPath('/api/plugin-ping') + (refresh ? '?refresh=1' : ''), { cache: 'no-store' });
    const data = await res.json();
    pluginPing.value = data.plugins || {};
    if (!data.ready) {
      window.clearTimeout(pingTimer);
      pingTimer = window.setTimeout(() => loadPluginPing(false), 4000);
    }
  } catch {
    // ping is optional
  }
};

const showExportModal = ref(false);
const openExportModal = () => (showExportModal.value = true);
const closeExportModal = () => (showExportModal.value = false);

const copyToClipboard = async (text: string, successMessage: string) => {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      alert(successMessage);
      return;
    }
  } catch {}

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  textarea.style.left = '-9999px';
  document.body.appendChild(textarea);
  textarea.select();
  const ok = document.execCommand('copy');
  document.body.removeChild(textarea);
  if (ok) alert(successMessage);
};

const copyPluginsConfig = async () => {
  const plugins = selectedPlugins.value.join(',');
  const content = plugins ? `export ENABLED_PLUGINS=${plugins}` : 'export ENABLED_PLUGINS=';
  await copyToClipboard(content, '插件配置已复制！');
};

onMounted(() => {
  initHealth();
  loadConfig();
  detectionSettings.value = loadDetectionSettings();
  loadPluginPing(false);
});
</script>

<template>
  <div class="config-container">
    <div class="config-header">
      <div>
        <h1 class="config-title">来源</h1>
        <p class="config-subtitle">仅插件来源：左点网络通不通，右点能不能搜到（用「电影」试搜）。</p>
      </div>

      <div class="stats-bar">
        <div class="stat-item">
          <span class="stat-label">插件</span>
          <span class="stat-value">{{ stats.plugins }}</span>
        </div>
        <div class="stat-item">
          <span class="stat-label">网盘</span>
          <span class="stat-value">{{ stats.diskTypes }}</span>
        </div>
      </div>
    </div>

    <div v-if="loading" class="loading-state">
      <div class="loading-spinner"></div>
      <p>加载配置中...</p>
    </div>

    <div v-else-if="error" class="error-state">
      <div class="error-icon">❌</div>
      <h3>加载失败</h3>
      <p>{{ error }}</p>
      <button @click="initHealth" class="retry-btn">重试</button>
    </div>

    <div v-else class="config-content">
      <div class="tabs-nav">
        <button class="tab-button" :class="{ active: activeTab === 'plugins' }" @click="activeTab = 'plugins'">
          <span class="tab-label">搜索插件</span>
          <span class="tab-count">{{ availablePlugins.length }}</span>
        </button>
        <button
          class="tab-button"
          :class="{ active: activeTab === 'diskTypes' }"
          @click="activeTab = 'diskTypes'"
        >
          <span class="tab-label">网盘类型</span>
          <span class="tab-count">{{ diskTypes.length }}</span>
        </button>
        <button
          class="tab-button"
          :class="{ active: activeTab === 'detection' }"
          @click="activeTab = 'detection'"
        >
          <span class="tab-label">检测</span>
          <span class="tab-status" :class="detectionSettings.enabled ? 'enabled' : 'disabled'">
            {{ detectionSettings.enabled ? '已开启' : '已关闭' }}
          </span>
        </button>
      </div>

      <div class="tab-content">
        <div v-show="activeTab === 'plugins'" class="tab-pane">
          <div class="pane-header">
            <div class="pane-title">
              <h3>搜索插件配置</h3>
              <span class="selected-count">已选 {{ selectedPlugins.length }} / {{ availablePlugins.length }}</span>
            </div>
            <div class="pane-actions">
              <button @click="toggleAllPlugins" class="action-btn">
                {{ selectedPlugins.length === availablePlugins.length ? '取消全选' : '全选' }}
              </button>
              <button @click.stop="loadPluginPing(true)" class="action-btn">重新检测</button>
            </div>
          </div>

          <div class="pane-content">
            <div class="items-grid">
              <div
                v-for="plugin in availablePlugins"
                :key="plugin"
                class="item-card"
                :class="{ selected: selectedPlugins.includes(plugin) }"
                @click="togglePlugin(plugin)"
              >
                <div class="item-name">{{ plugin }}</div>
                <div class="ping-dots" :title="'左网络 / 右搜索'">
                  <span class="ping-dot" :class="'is-' + pingNet(plugin)"></span>
                  <span class="ping-dot" :class="'is-' + pingUse(plugin)"></span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div v-show="activeTab === 'diskTypes'" class="tab-pane">
          <div class="pane-header">
            <div class="pane-title">
              <h3>网盘类型配置</h3>
              <span class="selected-count">已选 {{ selectedDiskTypes.length }} / {{ diskTypes.length }}</span>
            </div>
            <div class="pane-actions">
              <button @click="toggleAllDiskTypes" class="action-btn">
                {{ selectedDiskTypes.length === diskTypes.length ? '取消全选' : '全选' }}
              </button>
            </div>
          </div>

          <div class="pane-content">
            <div class="items-grid">
              <div
                v-for="diskType in diskTypes"
                :key="diskType.id"
                class="item-card"
                :class="{ selected: selectedDiskTypes.includes(diskType.id) }"
                @click="toggleDiskType(diskType.id)"
              >
                <div class="item-name">{{ diskType.name }}</div>
              </div>
            </div>
          </div>
        </div>

        <div v-show="activeTab === 'detection'" class="tab-pane">
          <div class="pane-header">
            <div class="pane-title">
              <h3>链接检测</h3>
            </div>
          </div>

          <div class="pane-content">
            <label class="detection-card" for="detection-toggle">
              <div class="detection-copy">
                <div class="detection-title">自动检测当前可见链接</div>
                <p class="detection-description">仅检测当前页面可见结果，降低额外开销。</p>
              </div>

              <div class="toggle-shell">
                <input id="detection-toggle" v-model="detectionSettings.enabled" type="checkbox" />
                <span class="toggle-track" :class="{ active: detectionSettings.enabled }"></span>
              </div>
            </label>
          </div>
        </div>
      </div>

      <div class="action-bar">
        <button class="export-btn" @click="openExportModal">导出插件配置</button>
        <button @click="resetToDefault" class="reset-btn">重置默认</button>
        <button @click="saveConfig" class="save-btn" :class="{ success: saveSuccess }">
          <span v-if="saveSuccess">✓ 已保存</span>
          <span v-else>保存配置</span>
        </button>
      </div>
    </div>

    <Teleport to="body">
      <div v-if="showExportModal" class="modal-overlay" @click="closeExportModal">
        <div class="modal-content" @click.stop>
          <div class="modal-header">
            <h2 class="modal-title">导出插件配置</h2>
            <button class="modal-close" @click="closeExportModal">关闭</button>
          </div>
          <div class="modal-body">
            <p>复制后可用于设置环境变量。</p>
            <div class="export-row">
              <button class="copy-btn" @click="copyPluginsConfig">复制</button>
            </div>
            <pre class="export-code">
{{ (() => {
  const plugins = selectedPlugins.value.join(',');
  return plugins ? `export ENABLED_PLUGINS=${plugins}` : 'export ENABLED_PLUGINS=';
})() }}
            </pre>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.config-container {
  max-width: 1100px;
  margin: 0 auto;
  padding: 2rem;
}
.config-header {
  display: flex;
  justify-content: space-between;
  gap: 1.5rem;
  flex-wrap: wrap;
  margin-bottom: 1.5rem;
  padding-bottom: 1rem;
  border-bottom: 2px solid hsl(var(--border));
}
.config-title {
  font-size: 1.5rem;
  font-weight: 900;
  color: hsl(var(--foreground));
}
.config-subtitle {
  margin-top: .5rem;
  color: hsl(var(--muted-foreground));
  max-width: 760px;
}
.stats-bar { display: flex; gap: 1rem; align-items: center; }
.stat-item {
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: .6rem;
  padding: .75rem 1rem;
  min-width: 150px;
}
.stat-label { display: block; color: hsl(var(--muted-foreground)); font-size: .9rem; }
.stat-value { display: block; color: hsl(var(--foreground)); font-weight: 900; font-size: 1.2rem; margin-top: .25rem; }

.loading-state, .error-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1rem;
  padding: 2rem;
}
.loading-spinner {
  width: 40px;
  height: 40px;
  border-radius: 9999px;
  border: 3px solid hsl(var(--border));
  border-top-color: hsl(var(--accent));
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
.error-icon { font-size: 2rem; }
.retry-btn {
  padding: .55rem 1rem;
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  border: none;
  border-radius: .4rem;
  cursor: pointer;
  font-weight: 800;
}

.tabs-nav { display: flex; gap: .75rem; flex-wrap: wrap; margin-bottom: 1.25rem; }
.tab-button {
  flex: 1 1 220px;
  background: transparent;
  border: 1px solid hsl(var(--border));
  border-radius: .6rem;
  padding: .85rem 1rem;
  cursor: pointer;
  color: hsl(var(--muted-foreground));
  transition: all .2s ease;
}
.tab-button.active { border-color: hsl(var(--accent)); color: hsl(var(--foreground)); background: hsl(var(--card)); }
.tab-label { font-weight: 900; }
.tab-count { margin-left: .5rem; color: hsl(var(--accent)); font-weight: 900; }
.tab-status.enabled { color: #22c55e; font-weight: 900; margin-left: .5rem; }
.tab-status.disabled { color: #eab308; font-weight: 900; margin-left: .5rem; }

.tab-pane {
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: .8rem;
  padding: 1.2rem;
}
.pane-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 1rem;
  margin-bottom: 1rem;
  flex-wrap: wrap;
}
.pane-title h3 { margin: 0; font-size: 1.1rem; font-weight: 900; }
.selected-count { display: inline-block; margin-top: .25rem; color: hsl(var(--muted-foreground)); font-weight: 700; font-size: .9rem; }
.pane-actions { display: flex; gap: .5rem; flex-wrap: wrap; }
.action-btn {
  background: transparent;
  border: 1px solid hsl(var(--border));
  border-radius: .5rem;
  padding: .55rem .8rem;
  cursor: pointer;
  font-weight: 900;
  color: hsl(var(--foreground));
}

.pane-content { margin-top: .75rem; }
.items-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: .75rem; }
.item-card {
  border: 1px solid hsl(var(--border));
  border-radius: .8rem;
  background: transparent;
  padding: .95rem 1rem;
  cursor: pointer;
  transition: all .2s ease;
}
.item-card.selected { border-color: hsl(var(--primary)); background: hsl(var(--primary)); color: hsl(var(--primary-foreground)); }
.item-name { font-weight: 900; margin-bottom: .6rem; }
.ping-dots { display: flex; justify-content: center; gap: 8px; margin-top: .4rem; }
.ping-dot { width: 9px; height: 9px; border-radius: 9999px; background: #94a3b8; }
.ping-dot.is-ok { background: #22c55e; }
.ping-dot.is-slow, .ping-dot.is-empty { background: #eab308; }
.ping-dot.is-down, .ping-dot.is-fail { background: #ef4444; }

.detection-card { display: flex; flex-direction: column; gap: .75rem; }
.detection-title { font-weight: 900; }
.detection-description { margin-top: .25rem; color: hsl(var(--muted-foreground)); }
.toggle-shell { display: flex; align-items: center; gap: .75rem; }
.toggle-track { width: 38px; height: 22px; border-radius: 9999px; background: hsl(var(--border)); }
.toggle-track.active { background: hsl(var(--primary)); }

.action-bar {
  display: flex;
  gap: .75rem;
  justify-content: flex-end;
  flex-wrap: wrap;
  margin-top: 1.25rem;
}
.export-btn {
  background: transparent;
  border: 1px solid hsl(var(--border));
  border-radius: .5rem;
  padding: .6rem .85rem;
  cursor: pointer;
  font-weight: 900;
  color: hsl(var(--foreground));
}
.reset-btn {
  background: transparent;
  border: 1px solid hsl(var(--border));
  border-radius: .5rem;
  padding: .6rem .85rem;
  cursor: pointer;
  font-weight: 900;
  color: hsl(var(--muted-foreground));
}
.save-btn {
  background: hsl(var(--primary));
  border: none;
  border-radius: .5rem;
  padding: .6rem .85rem;
  cursor: pointer;
  font-weight: 900;
  color: hsl(var(--primary-foreground));
}
.save-btn.success { filter: brightness(1.05); }

.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, .65);
  z-index: 9999;
  display: flex;
  align-items: center;
  justify-content: center;
}
.modal-content {
  width: min(720px, calc(100vw - 2rem));
  background: hsl(var(--card));
  border: 1px solid hsl(var(--border));
  border-radius: .8rem;
  padding: 1rem;
}
.modal-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: .75rem; }
.modal-title { font-weight: 900; }
.modal-close {
  border: 1px solid hsl(var(--border));
  background: transparent;
  border-radius: .5rem;
  padding: .35rem .6rem;
  cursor: pointer;
  font-weight: 800;
}
.export-code {
  background: rgba(0,0,0,.2);
  border: 1px solid hsl(var(--border));
  border-radius: .6rem;
  padding: .8rem;
  overflow: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  color: hsl(var(--foreground));
}
</style>
