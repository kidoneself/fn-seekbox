<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { getUpdateStatus, runUpdateAction, setAutoUpdate, updateError, type UpdateStatus } from '@/api/update';
const emit = defineEmits<{ close: []; version: [version: string] }>();
const state = ref<UpdateStatus | null>(null);
const busy = ref(false);
const error = ref('');
const reconnecting = ref(false);
const buildVersion = __APP_VERSION__;
let stopped = false;
let timer: ReturnType<typeof setTimeout> | undefined;
const labels: Record<string, string> = { disabled: '尚未配置发布源', waiting_idle: '等待当前任务结束', prepared: '准备切换版本', rolling_back: '正在恢复上一版', recovery_failed: '恢复未完成，请联系管理员', committed: '更新完成', error: '更新未完成', idle: '等待检查', checking: '正在检查新版', available: '发现新版本', downloading: '正在下载更新', verifying: '正在校验更新', staging: '正在准备新版', preparing: '等待当前任务结束', draining: '等待当前任务结束', installing: '正在更新', restarting: '正在重新启动', succeeded: '更新完成', success: '更新完成', failed: '更新未完成', rolled_back: '已恢复上一版' };
const phaseLabel = computed(() => labels[state.value?.phase || ''] || state.value?.phase || '读取版本信息');
const inProgress = computed(() => ['waiting_idle', 'prepared', 'rolling_back', 'checking', 'downloading', 'verifying', 'staging', 'preparing', 'draining', 'installing', 'restarting'].includes(state.value?.phase || ''));
const canAct = computed(() => state.value?.enabled && state.value?.can_manage && !busy.value && !inProgress.value);
const needsReload = computed(() => state.value?.current_version && state.value.current_version !== buildVersion && state.value.current_version !== 'dev');
const checkedAt = computed(() => {
  const value = state.value?.last_checked_at;
  if (!value) return '尚未检查';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '尚未检查' : date.toLocaleString('zh-CN', { hour12: false });
});
function accept(value: UpdateStatus) {
  state.value = value;
  if (value.current_version) emit('version', value.current_version);
}
async function load() {
  try { const value = await getUpdateStatus(); if (!stopped) { accept(value); reconnecting.value = false; } }
  catch { if (!stopped) reconnecting.value = true; }
  finally { if (!stopped) timer = setTimeout(load, 2500); }
}
async function act(action: 'check' | 'install') {
  if (!canAct.value) return;
  busy.value = true; error.value = '';
  try { accept(await runUpdateAction(action)); }
  catch (e) { error.value = updateError(e); }
  finally { busy.value = false; }
}
async function toggle(event: Event) {
  if (!state.value?.can_manage || busy.value) return;
  const input = event.target as HTMLInputElement;
  busy.value = true; error.value = '';
  try { accept(await setAutoUpdate(input.checked)); }
  catch (e) { error.value = updateError(e); input.checked = !!state.value?.auto_enabled; }
  finally { busy.value = false; }
}
function reload() { window.location.reload(); }
onMounted(load);
onUnmounted(() => { stopped = true; clearTimeout(timer); });
</script>

<template>
  <main class="update-page" aria-label="软件更新">
    <header class="update-heading"><div><h1>软件更新</h1><p>查看当前版本和更新进度。</p></div><button type="button" @click="emit('close')">返回搜索</button></header>
    <section class="update-overview">
      <div><span class="muted">当前运行版本</span><strong class="version">{{ state?.current_version === 'dev' ? '开发版' : `v${state?.current_version || buildVersion}` }}</strong><span class="muted">上次检查：{{ checkedAt }}</span></div>
      <div class="update-actions"><span class="phase" role="status">{{ reconnecting ? '等待服务恢复连接' : phaseLabel }}</span><button type="button" :disabled="!canAct" @click="act('check')">检查更新</button></div>
    </section>
    <p v-if="error || state?.error" class="update-error" role="alert">{{ error || state?.error }}</p>
    <p v-if="reconnecting" class="update-note">程序重启时连接会短暂中断，此页面会自动重试。</p>
    <section v-if="needsReload" class="update-reload"><p>新版程序已启动，刷新页面即可加载配套页面。</p><button type="button" @click="reload">刷新页面</button></section>
    <section v-if="state?.available && state.release" class="update-release">
      <div class="release-heading"><h2>新版本 v{{ state.release.version }}</h2><span class="muted">{{ (state.release.size / 1024 / 1024).toFixed(1) }} MB</span></div>
      <p class="release-notes">{{ state.release.notes || '此版本暂无更新说明。' }}</p>
      <button class="primary" type="button" :disabled="!canAct" @click="act('install')">立即更新</button><span class="install-note">等待当前任务结束后更新，账号和记录保留。</span>
    </section>
    <section class="update-preferences">
      <label><input type="checkbox" :checked="state?.auto_enabled" :disabled="!state?.can_manage || busy || inProgress" @change="toggle">自动更新</label>
      <p>每天北京时间 03:00 后错峰检查并更新；正在执行转存或扫码登录时会等待。</p>
      <p v-if="state && !state.enabled">发布源尚未就绪，配置完成后可使用在线更新。</p>
      <p v-if="state && !state.can_manage">当前账号可查看版本，更新操作由应用更新管理员管理。</p>
    </section>
  </main>
</template>

<style scoped>
.update-page { flex: 1; min-height: 0; overflow-y: auto; padding: 24px 28px; color: hsl(var(--foreground)); }
.update-heading,.update-overview,.release-heading,.update-reload { display: flex; justify-content: space-between; gap: 20px; align-items: center; }
h1 { margin: 0; font-size: 24px; font-weight: 650; } h2 { margin: 0; font-size: 17px; }
.update-heading p,.muted,.update-preferences p,.install-note { color: hsl(var(--muted-foreground)); font-size: 13px; }
.update-heading p { margin: 6px 0 0; }
.update-overview,.update-release,.update-preferences { margin-top: 22px; padding: 22px; border: 1px solid hsl(var(--border)); border-radius: 9px; background: hsl(var(--card)); }
.update-overview > div:first-child { display: flex; flex-direction: column; gap: 8px; }
.version { font-size: 28px; font-weight: 650; font-variant-numeric: tabular-nums; }
.update-actions { display: flex; align-items: center; gap: 16px; }
.phase { font-size: 13px; color: hsl(var(--primary)); }
button { border: 1px solid hsl(var(--border)); border-radius: 6px; padding: 9px 14px; background: hsl(var(--card)); color: hsl(var(--foreground)); font-size: 13px; cursor: pointer; white-space: nowrap; }
button.primary { background: hsl(var(--primary)); border-color: hsl(var(--primary)); color: white; }
button:disabled { opacity: .45; cursor: default; }
button:focus-visible,input:focus-visible { outline: 2px solid hsl(var(--primary)); outline-offset: 3px; }
.release-notes { margin: 18px 0 22px; font-size: 14px; line-height: 1.8; white-space: pre-wrap; overflow-wrap: anywhere; }
.install-note { margin-left: 14px; }
.update-preferences label { display: flex; align-items: center; gap: 9px; font-size: 14px; font-weight: 550; }
.update-preferences input { accent-color: hsl(var(--primary)); }
.update-preferences p { margin: 10px 0 0; line-height: 1.7; }
.update-error { color: #a33b22; font-size: 13px; overflow-wrap: anywhere; }.update-note { color: hsl(var(--muted-foreground)); font-size: 13px; }
.update-reload { padding: 12px 16px; margin-top: 16px; background: hsl(var(--primary) / .06); border-radius: 6px; font-size: 13px; }
@media(max-width:600px) { .update-page { padding: 18px 12px; } .update-overview { align-items: stretch; flex-direction: column; } .update-overview,.update-release,.update-preferences { padding: 16px; } .update-actions { justify-content: space-between; } .install-note { display: block; margin: 12px 0 0; } .update-reload { align-items: start; flex-direction: column; } }
</style>
