<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import QRCode from 'qrcode';
import { providerLogos } from '@/utils/providerLogos';
import { getDiskTypeName } from '@/utils/diskTypes';
import { useCloudDialog } from '@/utils/cloudDialog';
import {
  checkCloudAccount, checkCloudSubscription, cloudError, cloudJobActive, cloudJobStatus, cloudTime,
  connectCloudAccount, deleteCloudAccount, deleteCloudSubscription, listCloudAccounts, listCloudJobs,
  listCloudProviders, listCloudSubscriptions, updateCloudSubscription,
  startCloudQR, getCloudQR, completeCloudQR, cancelCloudQR, type CloudQR,
  type CloudAccount, type CloudJob, type CloudProvider, type CloudSubscription, type ProviderInfo,
} from '@/api/cloud';

const props = withDefaults(defineProps<{ initialTab?: 'accounts' | 'subscriptions' | 'jobs'; initialProvider?: CloudProvider; autoConnect?: boolean; page?: boolean }>(), { initialTab: 'accounts' });
const emit = defineEmits<{ close: []; connected: [] }>();
const dialogElement = ref<HTMLElement | null>(null);
useCloudDialog(dialogElement, computed(() => !props.page));
const tab = ref(props.initialTab);
const centerTabs = [{ id: 'accounts', name: '网盘账号' }, { id: 'subscriptions', name: '我的追更' }, { id: 'jobs', name: '转存记录' }] as const;
const providers = ref<ProviderInfo[]>([]);
const accounts = ref<CloudAccount[]>([]);
const subscriptions = ref<CloudSubscription[]>([]);
const jobs = ref<CloudJob[]>([]);
const loading = ref(true);
const refreshing = ref(false);
const busy = ref('');
const error = ref('');
const message = ref('');
const formOpen = ref(false);
const authMethod = ref<'qr' | 'manual'>('qr');
const qr = ref<CloudQR | null>(null);
const qrImage = ref('');
let qrTimer: ReturnType<typeof setTimeout> | undefined;
let qrGeneration = 0;
const editingID = ref('');
const selectedProvider = ref<CloudProvider>(props.initialProvider || 'quark');
const accountName = ref('');
const credentials = ref<Record<string, string>>({});
const loginMode = ref<'headers' | 'session'>('headers');
const sessionVisible = ref(false);
const removeAccountID = ref('');
const removeSubscriptionID = ref('');
const intervalEdits = ref<Record<string, number>>({});
let disposed = false;
let timer: ReturnType<typeof setTimeout> | undefined;
const provider = computed(() => providers.value.find(item => item.id === selectedProvider.value));
const supportsSession = computed(() => provider.value?.id === 'xunlei' && provider.value.credential_fields.some(field => field.key === 'web_session'));
const activeCredentialFields = computed(() => (provider.value?.credential_fields || []).filter(field => !supportsSession.value || (loginMode.value === 'session' ? field.key === 'web_session' : field.key !== 'web_session')));
const credentialsComplete = computed(() => activeCredentialFields.value.length > 0 && activeCredentialFields.value.every(field => (field.optional && field.key !== 'web_session') || credentials.value[field.key]?.trim()));
const hasActive = computed(() => jobs.value.some(cloudJobActive) || subscriptions.value.some(item => item.enabled));
watch([selectedProvider, loginMode], () => { credentials.value = {}; sessionVisible.value = false; }, { flush: 'sync' });
watch(selectedProvider, () => { stopQR(); }, { flush: 'sync' });

const stopQR = () => {
  ++qrGeneration; clearTimeout(qrTimer);
  const id = qr.value?.id; qr.value = null; qrImage.value = '';
  if (id) void cancelCloudQR(id).catch(() => {});
};
const renderQR = async (state: CloudQR, generation: number) => {
  const image = state.image || (state.qr_url ? await QRCode.toDataURL(state.qr_url, { width: 240, margin: 2 }) : '');
  if (disposed || generation !== qrGeneration) return;
  qr.value = state; qrImage.value = image;
};
const pollQR = async (id: string, generation: number) => {
  if (disposed || generation !== qrGeneration) return;
  try {
    let state = await getCloudQR(id);
    if (disposed || generation !== qrGeneration) return;
    if (state.status === 'ready') {
      busy.value = 'qr-complete';
      state = await completeCloudQR(id, accountName.value.trim());
      if (!disposed && generation === qrGeneration) busy.value = '';
    }
    await renderQR(state, generation);
    if (disposed || generation !== qrGeneration) return;
    if (state.status === 'connected') {
      stopQR(); formOpen.value = false; editingID.value = '';
      message.value = '扫码登录成功，账号已保存，可以转存和追更。';
      emit('connected'); await refresh(); return;
    }
    if (!['failed','expired','cancelled'].includes(state.status)) qrTimer = setTimeout(() => pollQR(id, generation), 1500);
  } catch (err) {
    if (!disposed && generation === qrGeneration) { busy.value = ''; error.value = cloudError(err); qrTimer = setTimeout(() => pollQR(id, generation), 4000); }
  }
};
const beginQR = async () => {
  if (busy.value || !provider.value?.qr_login) return;
  stopQR(); const generation = qrGeneration;
  busy.value = 'qr-start'; error.value = ''; message.value = '';
  try {
    const state = await startCloudQR({ provider: selectedProvider.value, account_id: editingID.value || undefined, name: accountName.value.trim() });
    if (disposed || generation !== qrGeneration) { void cancelCloudQR(state.id).catch(() => {}); return; }
    await renderQR(state, generation);
    qrTimer = setTimeout(() => pollQR(state.id, generation), 1000);
  } catch (err) { if (!disposed && generation === qrGeneration) error.value = cloudError(err); }
  finally { if (!disposed && generation === qrGeneration) busy.value = ''; }
};
const selectAuth = (method: 'qr' | 'manual') => { stopQR(); credentials.value = {}; authMethod.value = method; if (method === 'qr') void beginQR(); };

const refresh = async (initial = false) => {
  if (disposed || refreshing.value) return;
  clearTimeout(timer);
  refreshing.value = true;
  try {
    const values = await Promise.all([listCloudAccounts(), listCloudSubscriptions(), listCloudJobs(), ...(initial ? [listCloudProviders()] : [])]);
    if (disposed) return;
    accounts.value = values[0] as CloudAccount[];
    subscriptions.value = values[1] as CloudSubscription[];
    jobs.value = values[2] as CloudJob[];
    if (initial) providers.value = values[3] as ProviderInfo[];
    for (const item of subscriptions.value) if (!(item.id in intervalEdits.value)) intervalEdits.value[item.id] = item.interval_minutes;
  } catch (err) {
    if (!disposed) error.value = cloudError(err);
  } finally {
    if (!disposed) {
      loading.value = false;
      refreshing.value = false;
      if (hasActive.value) timer = setTimeout(() => refresh(), 8000);
    }
  }
};

const openForm = (account?: CloudAccount, providerID?: CloudProvider) => {
  stopQR();
  credentials.value = {};
  editingID.value = account?.id || '';
  accountName.value = account?.name || '';
  selectedProvider.value = account?.provider || providerID || selectedProvider.value;
  loginMode.value = 'headers';
  sessionVisible.value = false;
  formOpen.value = true;
  message.value = '';
  error.value = '';
  authMethod.value = 'qr';
  void beginQR();
};
const openProvider = (id: CloudProvider) => {
  if (busy.value) return;
  openForm(undefined, id);
};
watch(() => props.initialTab, value => { if (value) { stopQR(); formOpen.value = false; credentials.value = {}; tab.value = value; } });
const closeForm = () => { if (!busy.value) { stopQR(); formOpen.value = false; credentials.value = {}; sessionVisible.value = false; editingID.value = ''; } };
const close = () => { if (!busy.value) { stopQR(); credentials.value = {}; sessionVisible.value = false; emit('close'); } };

const connect = async () => {
  if (!credentialsComplete.value || busy.value) return;
  busy.value = 'connect';
  error.value = '';
  message.value = '';
  const selectedCredentials = Object.fromEntries(activeCredentialFields.value.filter(field => credentials.value[field.key]?.trim()).map(field => [field.key, credentials.value[field.key].trim()]));
  const payload = { provider: selectedProvider.value, name: accountName.value.trim() || provider.value!.name, credentials: selectedCredentials };
  credentials.value = {};
  sessionVisible.value = false;
  try {
    await connectCloudAccount(payload, editingID.value || undefined);
    if (disposed) return;
    formOpen.value = false;
    editingID.value = '';
    message.value = '账号已通过网盘校验，可以转存和追更。';
    emit('connected');
    await refresh();
  } catch (err) {
    if (!disposed) error.value = cloudError(err);
  } finally {
    Object.keys(payload.credentials).forEach(key => { payload.credentials[key] = ''; });
    if (!disposed) busy.value = '';
  }
};

const action = async (key: string, callback: () => Promise<unknown>, success: string) => {
  if (busy.value) return;
  busy.value = key;
  message.value = '';
  error.value = '';
  clearTimeout(timer);
  try {
    await callback();
    if (!disposed) message.value = success;
  } catch (err) {
    if (!disposed) error.value = cloudError(err);
  } finally {
    if (!disposed) {
      busy.value = '';
      removeAccountID.value = '';
      removeSubscriptionID.value = '';
      await refresh();
    }
  }
};

const accountStatus = (value: string) => ({ ready: '已连接', expired: '登录已失效', error: '连接异常' }[value] || value);
const subscriptionStatus = (item: CloudSubscription) => !item.enabled ? '已暂停' : ({ idle: '等待检查', running: '正在检查', paused: '已暂停', error: '需要处理' }[item.status] || item.status);
const accountLabel = (id: string) => accounts.value.find(item => item.id === id)?.name || '账号已移除';
const intervalValid = (id: string) => Number.isInteger(intervalEdits.value[id]) && intervalEdits.value[id] >= 15 && intervalEdits.value[id] <= 1440;
onMounted(async () => { await refresh(true); if (!disposed && props.autoConnect && providers.value.length && !accounts.value.some(a => a.provider === selectedProvider.value && a.status === 'ready')) openForm(); });
onUnmounted(() => { disposed = true; stopQR(); credentials.value = {}; clearTimeout(timer); });
</script>

<template>
  <Teleport to="body" :disabled="page">
    <div :class="page ? 'cloud-page' : 'cloud-overlay'" @click.self="!page && close()" @keydown.esc.stop="!page && close()">
      <section ref="dialogElement" :class="page ? 'cloud-page-content' : 'cloud-dialog wide'" :role="page ? 'region' : 'dialog'" :aria-modal="page ? undefined : true" aria-labelledby="cloud-center-title" tabindex="-1">
        <header class="cloud-header"><div><h2 id="cloud-center-title">{{ tab === 'accounts' ? '我的网盘' : tab === 'subscriptions' ? '我的追更' : '转存记录' }}</h2><p>{{ tab === 'accounts' ? '管理百度、夸克和迅雷账号。' : tab === 'subscriptions' ? '持续关注分享更新，新的内容自动保存到网盘。' : '查看每次转存的进度与结果。' }}</p></div><button v-if="!page" class="cloud-close" type="button" aria-label="关闭网盘中心" :disabled="!!busy" @click="close">×</button><div v-else class="cloud-header-actions"><button class="cloud-button" type="button" :disabled="refreshing || !!busy" @click="error = ''; refresh(!providers.length)">{{ refreshing ? '刷新中…' : '刷新' }}</button><button class="cloud-button" type="button" @click="close">← 返回搜索</button></div></header>
        <nav v-if="!page" class="cloud-tabs" aria-label="网盘中心导航"><button v-for="item in centerTabs" :key="item.id" type="button" class="cloud-tab" :class="{ active: tab === item.id }" :aria-current="tab === item.id ? 'page' : undefined" @click="tab = item.id">{{ item.name }}</button></nav>
        <div class="cloud-body">
          <div v-if="!page" class="cloud-row"><span class="cloud-help">{{ tab === 'accounts' ? '百度、夸克、迅雷账号分别连接，支持同网盘转存。' : tab === 'subscriptions' ? '持续检查原分享中的新增文件，关闭页面后也会继续。' : '仅以网盘实际返回结果判断是否完成。' }}</span><button class="cloud-button" type="button" :disabled="refreshing || !!busy" @click="error = ''; refresh(!providers.length)">{{ refreshing ? '刷新中…' : '刷新' }}</button></div>
          <p v-if="error" class="cloud-error" role="alert">{{ error }}</p>
          <p v-if="message" class="cloud-success" role="status">{{ message }}</p>
          <p v-if="loading" class="cloud-empty" role="status">正在读取网盘数据…</p>
          <template v-else-if="tab === 'accounts'">
            <div v-if="!page" class="cloud-actions" style="margin-top: .8rem"><button class="cloud-button primary" type="button" :disabled="!!busy || !providers.length" @click="openForm()">连接网盘账号</button></div>
            <div v-if="page" class="cloud-provider-grid"><button v-for="item in providers" :key="item.id" class="cloud-provider" :class="{ selected: formOpen && selectedProvider === item.id }" type="button" :disabled="!!busy" @click="openProvider(item.id)"><img class="cloud-provider-mark" :src="providerLogos[item.id]" :alt="item.name + ' Logo'" width="38" height="38"><span><strong>{{ item.name }}</strong><small>{{ accounts.filter(a => a.provider === item.id && a.status === 'ready').length ? '已连接 ' + accounts.filter(a => a.provider === item.id && a.status === 'ready').length + ' 个账号' : '尚未连接' }}</small></span><span class="cloud-provider-link">连接账号 ↗</span></button></div>
            <form v-if="formOpen && provider" class="cloud-card cloud-connection" autocomplete="off" @submit.prevent="connect">
              <div class="cloud-row"><h3>{{ editingID ? '重新连接账号' : '连接自己的网盘账号' }}</h3><button class="cloud-button" type="button" :disabled="!!busy" @click="closeForm">取消</button></div>
              <div class="connection-options"><label class="cloud-field connection-provider-field"><span>网盘</span><select v-model="selectedProvider" class="cloud-input" :disabled="!!editingID || !!busy" @change="authMethod === 'qr' && beginQR()"><option v-for="item in providers" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
              <label class="cloud-field"><span>账号备注</span><input v-model="accountName" class="cloud-input" :placeholder="provider.name + '账号'" maxlength="80" :disabled="!!busy" autocomplete="off"></label>
              <div class="cloud-actions"><button type="button" class="cloud-button" :class="{ primary: authMethod === 'qr' }" :disabled="!!busy" @click="selectAuth('qr')">扫码登录</button><button type="button" class="cloud-button" :class="{ primary: authMethod === 'manual' }" :disabled="!!busy" @click="selectAuth('manual')">手动导入</button></div>
              <p v-if="page" class="cloud-help connection-hint">使用{{ provider.name }} App 扫码，确认后自动保存账号。</p>
              </div><div class="connection-auth">
              <div v-if="authMethod === 'qr'" class="cloud-qr-login" aria-live="polite">
                <template v-if="provider.qr_login">
                  <img v-if="qrImage && !['failed','expired','cancelled'].includes(qr?.status || '')" :src="qrImage" :alt="provider.name + '官方登录二维码'" width="240" height="240">
                  <div v-else class="cloud-qr-placeholder">{{ qr?.status === 'failed' || qr?.status === 'expired' ? '请刷新二维码' : busy === 'qr-start' || qr?.status === 'starting' ? '正在获取二维码…' : '扫码登录' }}</div>
                  <p>{{ busy === 'qr-complete' ? '正在校验并保存账号…' : qr?.message || '使用对应网盘 App 扫码，在手机上确认登录。' }}</p>
                  <button type="button" class="cloud-button" :disabled="!!busy" @click="beginQR">刷新二维码</button>
                  <p v-if="!page" class="cloud-help">确认后自动保存到当前飞牛用户，仅用于你的转存和追更。无需填写网盘密码。</p>
                </template>
                <p v-else class="cloud-error">扫码登录组件尚未就绪，请更新应用；也可以切换到手动导入。</p>
              </div>
              <template v-else>
              <label v-if="supportsSession" class="cloud-field"><span>连接方式</span><select v-model="loginMode" class="cloud-input" :disabled="!!busy"><option value="headers">请求头凭据</option><option value="session">完整登录会话</option></select></label>
              <details class="cloud-note" open><summary>如何获取自己的登录凭据</summary><p>先在浏览器打开<a :href="provider.website" target="_blank" rel="noopener noreferrer">{{ provider.name }}官网</a>并登录自己的账号。<template v-if="!supportsSession || loginMode === 'headers'">按 F12 打开开发者工具，选择「网络 / Network」，刷新页面后查看网盘请求的「请求标头 / Request Headers」。</template></p><p v-if="supportsSession && loginMode === 'session'">填写本人已登录迅雷云盘的完整 web_session JSON。有有效的 refresh_token 及所需验证信息时，应用会尝试自动续期；缺少续期条件或会话已失效时，仍需重新连接。请保留完整字段，不要只填写单个访问令牌。</p><p v-else>{{ provider.help }}</p><p>将下方字段对应的值复制到这里，不需要提供账号密码。登录凭据具有账号访问权限，只填写自己的凭据，不要分享给他人。切换连接方式或提交后输入内容立即清空，不保存在浏览器；账号列表不会显示凭据。</p></details>
              <label v-for="field in activeCredentialFields" :key="provider.id + field.key" class="cloud-field"><span>{{ field.label }}</span><textarea v-if="field.key === 'web_session' && sessionVisible" v-model="credentials[field.key]" class="cloud-input" autocomplete="off" autocapitalize="off" spellcheck="false" required :disabled="!!busy" :aria-label="field.label" rows="6"></textarea><input v-else v-model="credentials[field.key]" class="cloud-input" type="password" autocomplete="new-password" autocapitalize="off" spellcheck="false" :required="!field.optional || field.key === 'web_session'" :disabled="!!busy" :aria-label="field.label"><small class="cloud-help">{{ field.help }}</small><button v-if="field.key === 'web_session'" class="cloud-button" type="button" :disabled="!!busy" :aria-pressed="sessionVisible" @click="sessionVisible = !sessionVisible">{{ sessionVisible ? '隐藏会话内容' : '显示多行内容' }}</button></label>
              <button class="cloud-button primary" type="submit" :disabled="!credentialsComplete || !!busy">{{ busy === 'connect' ? '正在校验账号…' : '校验并连接' }}</button>
              </template>
              </div>
            </form>
            <p v-if="!accounts.length && !formOpen" class="cloud-empty">还没有连接网盘账号。连接后即可在搜索结果中转存或添加追更。</p>
            <article v-for="account in accounts" :key="account.id" class="cloud-card">
              <div class="cloud-row"><div><h3>{{ account.name }}</h3><p class="cloud-help">{{ getDiskTypeName(account.provider) }}网盘</p></div><span class="cloud-badge" :class="account.status === 'ready' ? 'ready' : 'error'">{{ accountStatus(account.status) }}</span></div>
              <p v-if="account.last_error" class="cloud-error">{{ account.last_error }}</p>
              <div class="cloud-actions"><button class="cloud-button" type="button" :disabled="!!busy" @click="action(account.id, () => checkCloudAccount(account.id), '账号状态已更新')">{{ busy === account.id ? '校验中…' : '检查登录' }}</button><button class="cloud-button" type="button" :disabled="!!busy" @click="openForm(account)">重新连接</button><button class="cloud-button danger" type="button" :disabled="!!busy" @click="removeAccountID = account.id">移除账号</button></div>
              <div v-if="removeAccountID === account.id" class="cloud-note"><p>确认移除这个账号？如有追更任务，请先移除对应追更。网盘中已保存的文件会保留。</p><div class="cloud-actions"><button type="button" class="cloud-button danger" :disabled="!!busy" @click="action('remove-account', () => deleteCloudAccount(account.id), '账号已移除')">确认移除</button><button type="button" class="cloud-button" @click="removeAccountID = ''">取消</button></div></div>
            </article>
          </template>
          <template v-else-if="tab === 'subscriptions'">
            <p v-if="!subscriptions.length" class="cloud-empty">还没有追更任务。在搜索结果中点击「追更」，选好账号和目录即可开始。</p>
            <article v-for="item in subscriptions" :key="item.id" class="cloud-card">
              <div class="cloud-row"><h3>{{ item.title || '分享资源' }}</h3><span class="cloud-badge" :class="item.last_error ? 'error' : item.enabled ? 'ready' : ''">{{ subscriptionStatus(item) }}</span></div>
              <p class="cloud-help">{{ getDiskTypeName(item.provider) }} · {{ accountLabel(item.account_id) }} · {{ item.destination_name || '/' }}</p>
              <p class="cloud-help">累计转存 {{ item.transferred }} 个文件 · 上次检查 {{ cloudTime(item.last_checked_at) }}<br>下次检查 {{ item.enabled ? cloudTime(item.next_check_at) : '已暂停' }}</p>
              <p v-if="item.last_error" class="cloud-error">{{ item.last_error }}</p>
              <div class="cloud-actions"><label class="cloud-help" :for="'interval-' + item.id">每</label><input :id="'interval-' + item.id" v-model.number="intervalEdits[item.id]" type="number" min="15" max="1440" step="1" class="cloud-input cloud-interval" :disabled="!!busy" aria-label="检查间隔（分钟）"><span class="cloud-help">分钟检查</span><button class="cloud-button" type="button" :disabled="!!busy || !intervalValid(item.id) || intervalEdits[item.id] === item.interval_minutes" @click="action(item.id, () => updateCloudSubscription(item.id, { interval_minutes: intervalEdits[item.id] }), '检查间隔已更新')">保存间隔</button></div>
              <div class="cloud-actions" style="margin-top: .7rem"><button class="cloud-button" type="button" :aria-pressed="!!item.filter_ads" :disabled="!!busy || item.status === 'running'" @click="action(item.id, () => updateCloudSubscription(item.id, { filter_ads: !item.filter_ads }), '广告过滤设置已更新，将用于后续检查')">广告过滤：{{ item.filter_ads ? '开' : '关' }}</button><button class="cloud-button" type="button" :disabled="!!busy || item.status === 'running'" @click="action(item.id, () => checkCloudSubscription(item.id), '检查任务已提交，请在转存记录查看实际结果')">立即检查</button><button class="cloud-button" type="button" :disabled="!!busy" @click="action(item.id, () => updateCloudSubscription(item.id, { enabled: !item.enabled }), item.enabled ? '追更已暂停，已开始的任务会继续完成' : '已恢复追更')">{{ item.enabled ? '暂停追更' : '恢复追更' }}</button><a class="cloud-button" :href="item.share_url" target="_blank" rel="noopener noreferrer">查看分享</a><button class="cloud-button danger" type="button" :disabled="!!busy" @click="removeSubscriptionID = item.id">移除追更</button></div>
              <div v-if="removeSubscriptionID === item.id" class="cloud-note"><p>确认停止并移除这条追更？网盘中已保存的文件会保留。</p><div class="cloud-actions"><button class="cloud-button danger" type="button" :disabled="!!busy" @click="action('remove-subscription', () => deleteCloudSubscription(item.id), '追更已移除')">确认移除</button><button class="cloud-button" type="button" @click="removeSubscriptionID = ''">取消</button></div></div>
            </article>
          </template>
          <template v-else>
            <p v-if="!jobs.length" class="cloud-empty">还没有转存记录。一次转存和追更检查的结果都会显示在这里。</p>
            <article v-for="job in jobs" :key="job.id" class="cloud-card">
              <div class="cloud-row"><h3>{{ job.title || '分享资源' }}</h3><span class="cloud-badge" :class="job.status === 'succeeded' ? 'ready' : ['failed', 'partial', 'interrupted'].includes(job.status) ? 'error' : ''">{{ cloudJobStatus(job.status) }}</span></div>
              <p class="cloud-help">{{ job.subscription_id ? '持续追更' : '一次转存' }} · {{ getDiskTypeName(job.provider) }} · {{ accountLabel(job.account_id) }} · {{ cloudTime(job.created_at) }}</p>
              <p class="cloud-help">已保存 {{ job.saved }} 个 · 已跳过 {{ job.skipped }} 个 · 已过滤广告 {{ job.filtered || 0 }} 个 · 共 {{ job.total }} 个</p>
              <p v-if="job.error" class="cloud-error">{{ job.error }}</p>
            </article>
          </template>
        </div>
        <footer v-if="!page" class="cloud-footer"><span v-if="hasActive" class="cloud-help" style="margin-right: auto">运行状态自动刷新</span><button class="cloud-button" type="button" :disabled="!!busy" @click="close">返回</button></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped src="@/assets/styles/cloud.css"></style>

<style scoped src="@/assets/styles/cloud-page.css"></style>
