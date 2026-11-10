<script setup>
import { computed, onMounted, ref } from 'vue'
const props = defineProps({ api: { type: Function, required: true } })
const data = ref({ items: [], version_distribution: {}, recent_reports: [], total_reports: 0, signing_key_ids: [], upload_limit_bytes: 268435456 })
const busy = ref(false)
const loading = ref(false)
const activeId = ref('')
const error = ref('')
const message = ref('')
const manifestFile = ref(null)
const artifactFile = ref(null)
const manifestInput = ref(null)
const artifactInput = ref(null)
const preview = ref(null)
const showUpload = ref(false)
const resultFilter = ref('')
const statusNames = { draft: '草稿', published: '已发布', withdrawn: '已撤回', success: '成功', failed: '失败', rolled_back: '已回滚' }
const reports = computed(() => data.value.recent_reports.filter(item => !resultFilter.value || item.status === resultFilter.value))
const distribution = computed(() => Object.entries(data.value.version_distribution).sort((a, b) => b[1] - a[1]))
const bytes = value => `${(value / 1024 / 1024).toFixed(1)} MB`
const date = value => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
async function load() {
  loading.value = true
  error.value = ''
  try { data.value = await props.api('/releases') }
  catch (e) { error.value = e.message }
  finally { loading.value = false }
}
async function pickManifest(event) {
  manifestFile.value = event.target.files?.[0] || null
  preview.value = null
  error.value = ''
  if (!manifestFile.value) return
  if (manifestFile.value.size > 65536) { error.value = '签名清单不能超过 64 KB。'; manifestFile.value = null; return }
  try {
    const envelope = JSON.parse(await manifestFile.value.text())
    const raw = Uint8Array.from(atob(envelope.payload), c => c.charCodeAt(0))
    preview.value = JSON.parse(new TextDecoder().decode(raw))
  } catch { error.value = '签名清单格式不正确，请选择打包工具生成的 manifest.json。'; manifestFile.value = null }
}
function pickArtifact(event) {
  artifactFile.value = event.target.files?.[0] || null
  if (artifactFile.value?.size > data.value.upload_limit_bytes) { error.value = '更新包不能超过 256 MB。'; artifactFile.value = null }
}
async function upload() {
  if (!manifestFile.value || !artifactFile.value || busy.value) return
  busy.value = true; error.value = ''; message.value = ''
  const form = new FormData()
  form.append('manifest', manifestFile.value)
  form.append('artifact', artifactFile.value)
  try {
    await props.api('/releases', { method: 'POST', body: form })
    manifestFile.value = null; artifactFile.value = null; preview.value = null
    manifestInput.value.value = ''; artifactInput.value.value = ''
    showUpload.value = false
    await load()
    message.value = '更新包已验证并存为草稿。点击“发布”后，客户端才会收到更新。'
  } catch (e) { error.value = e.message }
  finally { busy.value = false }
}
async function change(item, action) {
  const prompt = action === 'publish'
    ? `发布 ${item.payload.version}（${item.payload.channel} / ${item.payload.arch}）？客户端将可下载并按自身设置安装。`
    : `撤回 ${item.payload.version}？后续查询与下载将停止，已安装的客户端不会自动降级。`
  if (!confirm(prompt)) return
  activeId.value = item.id; error.value = ''; message.value = ''
  try {
    await props.api(`/releases/${encodeURIComponent(item.id)}/${action}`, { method: 'POST', body: '{}' })
    await load()
    message.value = action === 'publish' ? '版本已发布。' : '版本已撤回。'
  } catch (e) { error.value = e.message }
  finally { activeId.value = '' }
}
onMounted(load)
</script>

<template>
  <section class="space-y-5" aria-label="版本发布">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-slate-500">上传签名更新包，先保存草稿，再发布到客户端。</p>
      <div class="flex gap-2">
        <button class="button-secondary" :disabled="loading || busy" @click="load">{{ loading ? '刷新中…' : '刷新' }}</button>
        <button class="button-primary" :disabled="busy" @click="showUpload = !showUpload">{{ showUpload ? '收起上传' : '上传新版本' }}</button>
      </div>
    </div>
    <p v-if="error" class="rounded-lg border border-red-100 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert">{{ error }}</p>
    <p v-if="message" class="rounded-lg border border-emerald-100 bg-emerald-50 px-4 py-3 text-sm text-emerald-800" role="status">{{ message }}</p>
    <form v-if="showUpload" class="panel space-y-4 p-5" @submit.prevent="upload">
      <div><h3 class="font-semibold">上传更新草稿</h3><p class="mt-1 text-xs leading-6 text-slate-500">选择打包工具生成的两个配套文件，服务器会校验签名、文件大小和 SHA256。账号数据不应包含在更新包中。</p></div>
      <p v-if="!data.signing_key_ids.length" class="rounded bg-amber-50 p-3 text-sm text-amber-800">服务端尚未配置发布公钥，配置后才能上传。</p>
      <div class="grid gap-4 md:grid-cols-2">
        <label class="field"><span class="field-label">签名清单 manifest.json</span><input ref="manifestInput" class="input !py-2 text-sm" type="file" accept=".json,application/json" :disabled="busy" required @change="pickManifest" /></label>
        <label class="field"><span class="field-label">更新包 tar.gz · 最大 256 MB</span><input ref="artifactInput" class="input !py-2 text-sm" type="file" accept=".gz,.tgz,application/gzip" :disabled="busy" required @change="pickArtifact" /></label>
      </div>
      <div v-if="preview" class="rounded-lg bg-slate-50 p-4 text-sm">
        <p class="font-medium">{{ preview.version }} <span class="ml-2 font-normal text-slate-500">{{ preview.channel }} / {{ preview.arch }} · {{ bytes(preview.size) }}</span></p>
        <p class="mt-2 whitespace-pre-wrap break-words text-slate-600">{{ preview.notes || '未填写更新说明' }}</p>
        <p class="mt-2 text-xs text-slate-400">清单预览；上传完成后才表示服务端校验通过。</p>
      </div>
      <button class="button-primary" :disabled="busy || !manifestFile || !artifactFile || !data.signing_key_ids.length" type="submit"><span v-if="busy" class="spinner" aria-hidden="true"></span>{{ busy ? '上传并校验中…' : '验证并保存草稿' }}</button>
    </form>
    <div class="panel p-5">
      <h3 class="text-sm font-semibold">客户端版本分布</h3>
      <p class="mt-1 text-xs text-slate-400">按最近心跳或更新结果统计，包含离线实例，不代表当前在线数量。</p>
      <div v-if="distribution.length" class="mt-4 flex flex-wrap gap-2"><span v-for="[version, count] in distribution" :key="version" class="rounded-md border border-slate-200 px-3 py-2 text-sm"><span class="font-mono">{{ version }}</span><strong class="ml-3 text-blue-600">{{ count }}</strong><span class="ml-1 text-xs text-slate-400">台</span></span></div>
      <p v-else class="mt-4 text-sm text-slate-400">还没有客户端上报。</p>
    </div>
    <div class="panel overflow-hidden">
      <div class="flex items-center justify-between border-b border-slate-100 px-5 py-4"><h3 class="text-sm font-semibold">发布记录</h3><span class="text-xs text-slate-400">{{ data.items.length }} 个版本</span></div>
      <p v-if="!data.items.length" class="p-8 text-center text-sm text-slate-400">暂无发布记录，上传更新包后会显示在这里。</p>
      <div v-for="item in data.items" :key="item.id" class="border-b border-slate-100 p-5 last:border-0">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2"><h4 class="font-mono text-lg font-semibold">{{ item.payload.version }}</h4><span class="rounded px-2 py-0.5 text-xs" :class="item.status==='published'?'bg-emerald-50 text-emerald-700':item.status==='withdrawn'?'bg-amber-50 text-amber-700':'bg-slate-100 text-slate-500'">{{ statusNames[item.status] }}</span><span class="text-xs text-slate-500">{{ item.payload.channel }} / {{ item.payload.arch }}</span></div>
            <p class="mt-2 whitespace-pre-wrap break-words text-sm text-slate-600">{{ item.payload.notes || '无更新说明' }}</p>
            <p class="mt-2 text-xs leading-6 text-slate-400">{{ bytes(item.payload.size) }} · 上传 {{ date(item.created_at) }}<span v-if="item.published_at"> · 发布 {{ date(item.published_at) }}</span><span v-if="item.withdrawn_at"> · 撤回 {{ date(item.withdrawn_at) }}</span></p>
            <details class="mt-2 text-xs text-slate-400"><summary class="cursor-pointer">包信息</summary><p class="mt-2 break-all font-mono">SHA256 {{ item.payload.sha256 }}</p><p class="mt-1">签名 {{ item.manifest.key_id }} · 最低更新器 {{ item.payload.min_bootstrap }} · 数据格式 {{ item.payload.data_schema }}</p></details>
          </div>
          <div class="flex shrink-0 flex-wrap items-center gap-3 text-xs">
            <span class="text-emerald-700">成功 {{ item.results.success }}</span><span class="text-red-600">失败 {{ item.results.failed }}</span><span class="text-amber-700">回滚 {{ item.results.rolled_back }}</span>
            <button v-if="item.status==='draft'" class="button-primary !px-3 !py-2 !text-xs" :disabled="!!activeId" @click="change(item,'publish')">{{ activeId===item.id?'处理中…':'发布' }}</button>
            <button v-if="item.status==='published'" class="button-secondary !px-3 !py-2 !text-xs" :disabled="!!activeId" @click="change(item,'withdraw')">{{ activeId===item.id?'处理中…':'撤回' }}</button>
          </div>
        </div>
      </div>
    </div>
    <div class="panel overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 px-5 py-4"><div><h3 class="text-sm font-semibold">更新结果</h3><p class="mt-1 text-xs text-slate-400">共 {{ data.total_reports }} 条，展示最近 100 条；重复上报只计一次。</p></div><select v-model="resultFilter" class="input !w-auto !py-2 text-xs" aria-label="筛选更新结果"><option value="">全部结果</option><option value="success">成功</option><option value="failed">失败</option><option value="rolled_back">已回滚</option></select></div>
      <div class="overflow-x-auto"><table class="w-full whitespace-nowrap text-left text-xs"><thead class="bg-slate-50 text-slate-500"><tr><th class="px-5 py-3 font-medium">时间</th><th class="px-5 py-3 font-medium">匿名实例</th><th class="px-5 py-3 font-medium">更新版本</th><th class="px-5 py-3 font-medium">结果</th><th class="px-5 py-3 font-medium">错误码</th></tr></thead><tbody><tr v-for="report in reports" :key="`${report.installation_id}:${report.event_id}`" class="border-t border-slate-100"><td class="px-5 py-3 text-slate-500">{{ date(report.occurred_at) }}</td><td class="px-5 py-3 font-mono" :title="report.installation_id">{{ report.installation_id.slice(0, 17) }}…</td><td class="px-5 py-3 font-mono">{{ report.from_version }} → {{ report.version }}</td><td class="px-5 py-3" :class="report.status==='success'?'text-emerald-700':report.status==='rolled_back'?'text-amber-700':'text-red-600'">{{ statusNames[report.status] }}</td><td class="px-5 py-3 font-mono text-slate-500">{{ report.error_code || '—' }}</td></tr><tr v-if="!reports.length"><td colspan="5" class="p-8 text-center text-slate-400">暂无更新结果</td></tr></tbody></table></div>
    </div>
  </section>
</template>
