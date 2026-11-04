<script setup lang="ts">
import { computed, onUnmounted, ref, toRef, watch } from 'vue';
import { useCloudDialog } from '@/utils/cloudDialog';
import {
  cloudError, cloudJobActive, cloudJobStatus, cloudRequestID, cloudRequestRejected, createCloudSubscription,
  createCloudTransfer, getCloudJob, listCloudAccounts, listCloudFolders, listCloudProviders,
  type CloudAccount, type CloudFolder, type CloudJob, type CloudTarget, type ProviderInfo, type TransferRequest,
} from '@/api/cloud';

const props = defineProps<{ target: CloudTarget; visible: boolean }>();
const emit = defineEmits<{ close: []; accounts: []; records: [tab: 'subscriptions' | 'jobs'] }>();
const dialogElement = ref<HTMLElement | null>(null);
useCloudDialog(dialogElement, toRef(props, 'visible'));
const providers = ref<ProviderInfo[]>([]);
const accounts = ref<CloudAccount[]>([]);
const accountID = ref('');
const folders = ref<CloudFolder[]>([]);
const path = ref<CloudFolder[]>([]);
const interval = ref(60);
const filterAds = ref(true);
const loading = ref(false);
const folderLoading = ref(false);
const folderReady = ref(false);
const submitting = ref(false);
const error = ref('');
const folderError = ref('');
const pollError = ref('');
const job = ref<CloudJob | null>(null);
const subscriptionID = ref('');
const attempted = ref(false);
const submissionRejected = ref(false);
const requestID = cloudRequestID();
let submittedBody: TransferRequest | null = null;
let submittedInterval = 60;
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let folderGeneration = 0;
let disposed = false;
const provider = computed(() => providers.value.find(item => item.id === props.target.provider));
const availableAccounts = computed(() => accounts.value.filter(item => item.provider === props.target.provider && item.status === 'ready'));
const currentFolder = computed(() => path.value[path.value.length - 1]);
const destinationName = computed(() => path.value.length > 1 ? '/' + path.value.slice(1).map(item => item.name).join('/') : '/');
const isSubscription = computed(() => props.target.mode === 'subscribe');
const title = computed(() => isSubscription.value ? '添加持续追更' : '转存到网盘');
const canSubmit = computed(() => attempted.value || (accountID.value && folderReady.value && !folderLoading.value && Number.isInteger(interval.value) && interval.value >= 15 && interval.value <= 1440));

const loadFolders = async (nextPath: CloudFolder[]) => {
  const generation = ++folderGeneration;
  folderLoading.value = true;
  folderError.value = '';
  try {
    const result = await listCloudFolders(accountID.value, nextPath[nextPath.length - 1].id);
    if (generation !== folderGeneration || disposed) return;
    folders.value = result;
    path.value = nextPath;
    folderReady.value = true;
  } catch (err) {
    if (generation === folderGeneration && !disposed) folderError.value = cloudError(err);
  } finally {
    if (generation === folderGeneration && !disposed) folderLoading.value = false;
  }
};

const resetFolder = () => {
  folderGeneration++;
  path.value = [];
  folders.value = [];
  folderReady.value = false;
  folderLoading.value = false;
  if (accountID.value && provider.value) loadFolders([{ id: provider.value.root_id, name: '根目录' }]);
};

const loadAccounts = async () => {
  loading.value = true;
  error.value = '';
  try {
    const [nextProviders, nextAccounts] = await Promise.all([listCloudProviders(), listCloudAccounts()]);
    if (disposed) return;
    providers.value = nextProviders;
    accounts.value = nextAccounts;
    const nextAccount = availableAccounts.value.find(item => item.id === accountID.value) || availableAccounts.value[0];
    if (accountID.value === (nextAccount?.id || '')) resetFolder();
    else accountID.value = nextAccount?.id || '';
  } catch (err) {
    if (!disposed) error.value = cloudError(err);
  } finally {
    if (!disposed) loading.value = false;
  }
};

const pollJob = async () => {
  if (!job.value || disposed || !props.visible) return;
  clearTimeout(pollTimer);
  try {
    const nextJob = await getCloudJob(job.value.id);
    if (disposed) return;
    job.value = nextJob;
    pollError.value = '';
  } catch (err) {
    if (!disposed) pollError.value = cloudError(err);
  }
  if (!disposed && props.visible && job.value && cloudJobActive(job.value)) pollTimer = setTimeout(pollJob, pollError.value ? 8000 : 2000);
};

const submit = async () => {
  if (!canSubmit.value || submitting.value || job.value) return;
  error.value = '';
  submissionRejected.value = false;
  submitting.value = true;
  if (!submittedBody) {
    submittedBody = {
      account_id: accountID.value, title: props.target.title, share_url: props.target.url,
      password: props.target.password || '', destination_id: currentFolder.value.id,
      destination_name: destinationName.value, request_id: requestID, filter_ads: filterAds.value,
    };
    submittedInterval = interval.value;
  }
  attempted.value = true;
  try {
    if (isSubscription.value) {
      const result = await createCloudSubscription({ ...submittedBody, interval_minutes: submittedInterval });
      if (disposed) return;
      subscriptionID.value = result.subscription.id;
      job.value = result.job;
    } else {
      const result = await createCloudTransfer(submittedBody);
      if (disposed) return;
      job.value = result;
    }
    pollJob();
  } catch (err) {
    if (!disposed) {
      error.value = cloudError(err);
      if (cloudRequestRejected(err)) {
        submissionRejected.value = true;
        attempted.value = false;
        submittedBody = null;
      }
    }
  } finally {
    if (!disposed) submitting.value = false;
  }
};

const close = () => { if (!submitting.value) emit('close'); };
watch(accountID, resetFolder);
watch(() => props.visible, visible => {
  clearTimeout(pollTimer);
  if (!visible) return;
  if (job.value) pollJob();
  else if (!attempted.value) loadAccounts();
}, { immediate: true });
onUnmounted(() => {
  disposed = true;
  folderGeneration++;
  clearTimeout(pollTimer);
  submittedBody = null;
});
</script>

<template>
  <Teleport to="body">
    <div v-if="visible" class="cloud-overlay" @click.self="close" @keydown.esc.stop="close">
      <section ref="dialogElement" class="cloud-dialog" role="dialog" aria-modal="true" aria-labelledby="cloud-transfer-title" tabindex="-1">
        <header class="cloud-header">
          <div><h2 id="cloud-transfer-title">{{ title }}</h2><p>{{ provider?.name || '网盘' }}分享 · {{ isSubscription ? '先保存现有文件，之后自动检查新增' : '保存当前分享中的文件，仅执行一次' }}</p></div>
          <button class="cloud-close" type="button" aria-label="关闭转存窗口" :disabled="submitting" @click="close">×</button>
        </header>
        <div class="cloud-body">
          <div class="cloud-summary"><strong>{{ target.title || '分享资源' }}</strong><small>{{ target.url }}</small></div>
          <p v-if="loading" class="cloud-help" role="status">正在读取已连接账号…</p>
          <p v-if="error" class="cloud-error" role="alert">{{ error }}</p>
          <p v-if="submissionRejected" class="cloud-help">服务端已拒绝这次请求，请根据上方原因检查账号和保存位置后重试。</p>
          <template v-if="!job">
            <div v-if="!loading && !availableAccounts.length && !error" class="cloud-empty">
              <p>请先连接一个{{ provider?.name }}账号，才能保存到自己的网盘。</p>
              <button type="button" class="cloud-button primary" @click="emit('accounts')">连接网盘账号</button>
            </div>
            <template v-if="availableAccounts.length">
              <label class="cloud-field"><span>保存到哪个账号</span><select v-model="accountID" class="cloud-input" :disabled="loading || submitting || attempted"><option v-for="account in availableAccounts" :key="account.id" :value="account.id">{{ account.name }}</option></select></label>
              <div class="cloud-field">
                <span>目标文件夹</span>
                <div class="cloud-path"><span>当前选择：{{ path.length ? destinationName : '正在读取根目录' }}</span><button type="button" class="cloud-button" :disabled="path.length < 2 || folderLoading || attempted" @click="loadFolders(path.slice(0, -1))">返回上级</button></div>
                <p v-if="folderLoading" class="cloud-help" role="status">正在读取网盘文件夹…</p>
                <div v-if="folderError" class="cloud-error" role="alert">{{ folderError }} <button type="button" class="cloud-button" :disabled="folderLoading || attempted" @click="path.length ? loadFolders(path) : resetFolder()">重试</button></div>
                <div v-if="folderReady" class="cloud-folder-list">
                  <button v-for="folder in folders" :key="folder.id" type="button" class="cloud-folder" :disabled="folderLoading || attempted" @click="loadFolders([...path, folder])"><span>▱ {{ folder.name }}</span><span aria-hidden="true">›</span></button>
                  <p v-if="!folders.length" class="cloud-empty">此处没有子文件夹，将保存到当前目录。</p>
                </div>
              </div>
              <div class="cloud-ad-filter"><label><input v-model="filterAds" type="checkbox" :disabled="loading || submitting || attempted">过滤广告附件</label><p class="cloud-help">按文件名跳过加群、扫码关注等推广文字、图片和链接。视频、字幕、压缩包和 PDF 保留，不删除已有文件。</p></div>
              <label v-if="isSubscription" class="cloud-field"><span>检查间隔（分钟）</span><input v-model.number="interval" type="number" min="15" max="1440" step="1" class="cloud-input cloud-interval" :disabled="attempted"><small class="cloud-help">15～1440 分钟，默认每 60 分钟检查一次。应用运行时，关闭页面后也会继续。</small></label>
              <p class="cloud-note">{{ isSubscription ? '确认后会立即转存现有文件，并持续检查这个分享链接，将新增文件保存到所选目录。分享失效后需处理，不会自动寻找其他分享。' : '确认后会实际写入所选网盘目录，不会下载到 NAS。' }} 仅支持同一家网盘转存。</p>
              <p v-if="attempted && error" class="cloud-help">请求结果尚未确认，重试将使用相同任务标识以避免重复提交。也可以先到转存记录查看。</p>
            </template>
          </template>
          <template v-else>
            <div :class="job.status === 'succeeded' ? 'cloud-success' : ['failed', 'partial', 'interrupted'].includes(job.status) ? 'cloud-error' : 'cloud-note'" role="status">
              <strong>{{ cloudJobStatus(job.status) }}</strong>
              <p v-if="subscriptionID">已加入追更列表。后续按设定间隔检查此分享。</p>
              <p v-if="cloudJobActive(job)">任务已提交，完成结果以网盘返回为准。可以关闭窗口，稍后在转存记录查看。</p>
              <p>已保存 {{ job.saved }} 个文件 · 已跳过 {{ job.skipped }} 个 · 已过滤广告 {{ job.filtered || 0 }} 个 · 共 {{ job.total }} 个</p>
              <p v-if="job.error">{{ job.error }}</p>
            </div>
            <div v-if="pollError" class="cloud-error" role="alert">状态刷新失败：{{ pollError }} <button type="button" class="cloud-button" @click="pollJob">刷新状态</button></div>
          </template>
        </div>
        <footer class="cloud-footer">
          <button v-if="!job && !attempted" class="cloud-button" type="button" :disabled="submitting" @click="emit('accounts')">管理账号</button>
          <button v-if="job || attempted" class="cloud-button" type="button" :disabled="submitting" @click="emit('records', subscriptionID ? 'subscriptions' : 'jobs')">{{ subscriptionID ? '查看追更列表' : '查看转存记录' }}</button>
          <button v-if="!job && availableAccounts.length" class="cloud-button primary" type="button" :disabled="!canSubmit || loading || submitting" @click="submit">{{ submitting ? '正在提交…' : attempted ? '重试同一请求' : isSubscription ? '确认并开始追更' : '确认转存一次' }}</button>
          <button v-else-if="job" class="cloud-button primary" type="button" @click="close">关闭</button>
          <button v-else-if="error" class="cloud-button" type="button" :disabled="loading" @click="loadAccounts">重新加载</button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped src="@/assets/styles/cloud.css"></style>
