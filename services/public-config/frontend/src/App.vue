<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import BusinessDashboard from './BusinessDashboard.vue'
import RecordsPanel from './RecordsPanel.vue'
import InterfaceCenter from './InterfaceCenter.vue'
import ClientSettings from './ClientSettings.vue'

const API = '/seekbox-config/api'
const publicOrigin = 'https://hermes.smartloan.cn'
const providers = [
  { provider: 'baidu', name: '百度网盘', initial: '百', color: 'bg-blue-50 text-blue-600', url: 'https://pan.baidu.com/s/…' },
  { provider: 'quark', name: '夸克网盘', initial: '夸', color: 'bg-violet-50 text-violet-600', url: 'https://pan.quark.cn/s/…' },
  { provider: 'xunlei', name: '迅雷云盘', initial: '迅', color: 'bg-cyan-50 text-cyan-600', url: 'https://pan.xunlei.com/s/…' },
]
const booting = ref(true)
const authenticated = ref(false)
const loginPassword = ref('')
const loginError = ref('')
const loginBusy = ref(false)
const logoutBusy = ref(false)
const tab = ref('stats')
const tabs = [['stats','使用数据'],['links','测试文件'],['sites','搜索站点']]
const advancedTabs = [['instances','匿名实例'],['accounts','账号状态'],['checks','检查记录'],['slots','每日计划'],['events','事件明细'],['center','接口中心'],['audit','调用记录'],['settings','上报设置']]
const loading = ref(false)
const loadError = ref('')
const globalError = ref('')
const loaded = ref(false)
const links = reactive({})
const sites = ref([])
const linkStates = reactive({})
const siteStates = reactive({})
const savedLinks = reactive({})
const savedSites = reactive({})
const search = ref('')
const showNewSite = ref(false)
const newSite = reactive({ id: '', name: '', url: '', enabled: true, order: 1 })
const newSiteState = reactive({ busy: false, message: '', error: false })
const copied = ref('')

const enabledSites = computed(() => Object.values(savedSites).filter((site) => site.enabled).length)
const enabledLinks = computed(() => Object.values(savedLinks).filter((link) => link.enabled).length)
const visibleSites = computed(() => {
  const query = search.value.trim().toLowerCase()
  return sites.value.filter((site) => !query || `${site.id} ${site.name} ${site.url}`.toLowerCase().includes(query))
    .slice().sort((a, b) => Number(a.order) - Number(b.order) || a.id.localeCompare(b.id))
})

const blankState = () => ({ busy: false, message: '', error: false })
const hasExample = (item) => /EXAMPLE_NOT_REAL/i.test(`${item?.share_url || ''} ${item?.file_path || ''}`)
const linkDirty = (provider) => JSON.stringify(links[provider]) !== JSON.stringify(savedLinks[provider])
const siteDirty = (id) => JSON.stringify(sites.value.find((site) => site.id === id)) !== JSON.stringify(savedSites[id])

async function api(path, options = {}) {
  let response
  try {
    response = await fetch(`${API}${path}`, {
      credentials: 'same-origin',
      cache: 'no-store',
      ...options,
      headers: { Accept: 'application/json', ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers },
    })
  } catch {
    throw new Error('无法连接服务，请检查网络后重试。')
  }
  if (response.status === 401) {
    const hadSession = authenticated.value
    authenticated.value = false
    loginPassword.value = ''
    if (hadSession) loginError.value = '登录已失效，请重新登录。未保存的编辑仍保留在本页。'
  }
  let payload
  try { payload = await response.json() } catch { throw new Error(response.status === 401 ? '登录已失效，请重新登录。' : `服务返回了无效响应（HTTP ${response.status}），请稍后重试。`) }
  if (!response.ok || payload.code !== 0) throw new Error(payload.message || `请求失败（HTTP ${response.status}）`)
  return payload.data
}

async function loadConfig() {
  loading.value = true
  loadError.value = ''
  try {
    const data = await api('/config')
    if (!data || !Array.isArray(data.check_links) || !Array.isArray(data.search_sites)) throw new Error('配置数据格式不正确，请稍后重试。')
    for (const provider of providers) {
      const item = data.check_links.find((link) => link.provider === provider.provider)
      const value = { source_id: item?.source_id || '', source_version: item?.source_version || '1', expires_at: item?.expires_at || null, provider: provider.provider, share_url: item?.share_url || '', password: item?.password || '', file_path: item?.file_path || '', enabled: item?.enabled === true }
      links[provider.provider] = { ...value }
      savedLinks[provider.provider] = { ...value }
      linkStates[provider.provider] = blankState()
    }
    sites.value = data.search_sites.map((site) => ({ id: site.id, name: site.name, url: site.url, enabled: site.enabled === true, order: site.order }))
    for (const key of Object.keys(savedSites)) delete savedSites[key]
    for (const site of sites.value) {
      savedSites[site.id] = { ...site }
      siteStates[site.id] = blankState()
    }
    loaded.value = true
  } catch (error) { loadError.value = error.message }
  finally { loading.value = false }
}

async function login() {
  if (!loginPassword.value || loginBusy.value) return
  loginBusy.value = true
  loginError.value = ''
  try {
    await api('/login', { method: 'POST', body: JSON.stringify({ password: loginPassword.value }) })
    loginPassword.value = ''
    authenticated.value = true
    // Re-authentication keeps unsaved forms; first login reads the server.
    if (!loaded.value) await loadConfig()
  } catch (error) { loginError.value = error.message }
  finally { loginBusy.value = false }
}

async function logout() {
  logoutBusy.value = true
  globalError.value = ''
  try {
    await api('/logout', { method: 'POST', body: '{}' })
    authenticated.value = false
    loaded.value = false
    loginPassword.value = ''
    loginError.value = ''
    sites.value = []
    showNewSite.value = false
    for (const key of Object.keys(links)) delete links[key]
  } catch (error) { globalError.value = error.message }
  finally { logoutBusy.value = false }
}

async function saveLink(provider) {
  const state = linkStates[provider]
  if (state.busy) return
  state.message = ''
  state.error = false
  const form = links[provider]
  const body = { source_id: form.source_id, expires_at: form.expires_at || null, share_url: form.share_url.trim(), password: form.password.trim(), file_path: form.file_path.trim(), enabled: form.enabled }
  if (body.enabled && hasExample(body)) {
    state.error = true
    state.message = '请先将示例链接和路径替换为真实测试源，再启用。'
    return
  }
  if (body.enabled && (!body.share_url || !body.file_path)) {
    state.error = true
    state.message = '启用前请填写真实分享链接和测试文件相对路径。'
    return
  }
  state.busy = true
  try {
    const result = await api(`/check-links/${provider}`, { method: 'PUT', body: JSON.stringify(body) })
    const saved = { provider, ...body, source_version: result.source_version }
    savedLinks[provider] = { ...saved }
    links[provider] = { ...saved }
    state.message = body.enabled ? '已保存并生效，公共接口现已使用此配置。' : '已保存并生效，此网盘当前不提供测试源。'
  } catch (error) { state.error = true; state.message = error.message }
  finally { state.busy = false }
}

async function deleteSite(site) {
  if (!confirm('移除此站点入口？历史关联保留，原ID不能复用。')) return
  try { await api('/sites/'+encodeURIComponent(site.id), {method:'DELETE',body:'{}'});sites.value=sites.value.filter(x=>x.id!==site.id);delete savedSites[site.id] } catch(e) {globalError.value=e.message}
}
function siteBody(form) {
  return { name: form.name.trim(), url: form.url.trim(), enabled: form.enabled, order: Number(form.order) }
}

function validateSite(body) {
  if (!body.name || !body.url) return '请填写站点名称和入口网址。'
  try {
    const url = new URL(body.url)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return '请输入不含账号密码的 HTTP 或 HTTPS 网址。'
  } catch { return '请输入完整的 HTTP 或 HTTPS 网址。' }
  if (!Number.isSafeInteger(body.order) || body.order < 0) return '排序值必须是大于或等于 0 的整数。'
  return ''
}

async function saveSite(site) {
  const state = siteStates[site.id]
  if (state.busy) return
  const body = siteBody(site)
  state.message = validateSite(body)
  state.error = Boolean(state.message)
  if (state.error) return
  state.busy = true
  try {
    await api(`/sites/${encodeURIComponent(site.id)}`, { method: 'PUT', body: JSON.stringify(body) })
    Object.assign(site, body)
    savedSites[site.id] = { ...site }
    state.message = '已保存并立即生效。'
  } catch (error) { state.error = true; state.message = error.message }
  finally { state.busy = false }
}

function openNewSite() {
  showNewSite.value = true
  newSite.order = Math.max(0, ...sites.value.map((site) => Number(site.order) || 0)) + 1
  newSiteState.message = ''
}

async function addSite() {
  if (newSiteState.busy) return
  const id = newSite.id.trim()
  const body = siteBody(newSite)
  newSiteState.message = !/^[a-zA-Z0-9][a-zA-Z0-9_-]*$/.test(id) ? '稳定标识请使用字母、数字、下划线或短横线，并以字母或数字开头。' : validateSite(body)
  if (sites.value.some((site) => site.id === id)) newSiteState.message = '此标识已存在，请使用唯一标识。'
  newSiteState.error = Boolean(newSiteState.message)
  if (newSiteState.error) return
  newSiteState.busy = true
  try {
    await api('/sites', { method: 'POST', body: JSON.stringify({ id, ...body }) })
    const created = { id, ...body }
    sites.value.push(created)
    savedSites[id] = { ...created }
    siteStates[id] = { ...blankState(), message: '已新增并立即生效。' }
    Object.assign(newSite, { id: '', name: '', url: '', enabled: true, order: 1 })
    showNewSite.value = false
    search.value = ''
  } catch (error) { newSiteState.error = true; newSiteState.message = error.message }
  finally { newSiteState.busy = false }
}

async function copyUrl(kind) {
  const url = publicOrigin + (kind === 'links' ? '/api/check-link?provider=baidu' : '/api/search-sites')
  try {
    await navigator.clipboard.writeText(url)
    copied.value = kind
    setTimeout(() => { if (copied.value === kind) copied.value = '' }, 2500)
  } catch { globalError.value = '复制失败，请长按或选中接口地址复制。' }
}

onMounted(async () => {
  try {
    await api('/session')
    authenticated.value = true
    await loadConfig()
  } catch (error) {
    if (!/登录|认证|鉴权|未授权|Unauthorized/i.test(error.message)) loginError.value = error.message
  } finally { booting.value = false }
})
</script>

<template>
  <div v-if="booting" class="flex min-h-screen items-center justify-center gap-3 text-sm text-slate-500" role="status">
    <span class="spinner text-blue-600" aria-hidden="true"></span> 正在连接配置服务…
  </div>

  <main v-else-if="!authenticated" class="flex min-h-screen items-center justify-center px-5 py-12">
    <div class="w-full max-w-sm">
      <div class="mb-8 text-center">
        <div class="mx-auto mb-5 flex h-14 w-14 items-center justify-center rounded-2xl bg-blue-600 text-white shadow-lg shadow-blue-600/20">
          <svg class="h-7 w-7" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7.5h.01M7 17h.01M12 7.5h5M12 17h5" stroke-linecap="round"/></svg>
        </div>
        <h1 class="text-xl font-semibold tracking-tight text-slate-900">小白搜盘 · 管理中心</h1>
        <p class="mt-2 text-sm text-slate-500">配置测试源与站点，查看使用人数和每天的检查次数</p>
      </div>
      <form class="panel p-6 sm:p-7" @submit.prevent="login">
        <label class="field" for="admin-password"><span class="field-label">管理员密码</span>
          <input id="admin-password" v-model="loginPassword" class="input" type="password" autocomplete="current-password" placeholder="输入后台管理员密码" required :disabled="loginBusy" autofocus />
        </label>
        <p v-if="loginError" class="mt-3 break-words text-sm text-red-600" role="alert">{{ loginError }}</p>
        <button class="button-primary mt-5 w-full" :disabled="loginBusy || !loginPassword" type="submit"><span v-if="loginBusy" class="spinner" aria-hidden="true"></span>{{ loginBusy ? '正在登录…' : '登录配置后台' }}</button>
      </form>
      <p class="mt-5 text-center text-xs leading-6 text-slate-400">公共接口无需登录，配置修改仅限管理员。<br />后台使用安全会话，不在本地保存密码。</p>
    </div>
  </main>

  <div v-else class="min-h-screen">
    <header class="border-b border-slate-200 bg-white">
      <div class="mx-auto flex max-w-7xl items-center justify-between gap-3 px-4 py-5 sm:px-8">
        <div class="flex min-w-0 items-center gap-3">
          <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-blue-600 text-white">
            <svg class="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7.5h.01M7 17h.01M12 7.5h5M12 17h5" stroke-linecap="round"/></svg>
          </div>
          <div class="min-w-0"><h1 class="text-sm font-semibold tracking-tight text-slate-900 sm:text-lg">小白搜盘 · 管理中心</h1><p class="mt-0.5 text-[11px] text-slate-400 sm:text-xs">hermes.smartloan.cn</p></div>
        </div>
        <button class="button-secondary shrink-0 !px-3 !py-2 !text-xs" :disabled="logoutBusy" @click="logout">{{ logoutBusy ? '退出中…' : '退出登录' }}</button>
      </div>
    </header>

    <main class="mx-auto max-w-7xl px-4 pb-12 pt-7 sm:px-8 sm:pt-9">
      <div class="mb-7">
        <h2 class="text-2xl font-semibold tracking-tight text-slate-900">{{ tab === 'stats' ? '有多少人在用，每天检查了多少次' : [...tabs, ...advancedTabs].find(item => item[0] === tab)?.[1] }}</h2>
        <p class="mt-2 text-sm leading-6 text-slate-500">{{ tab === 'stats' ? '看人数、看账号、看成功次数，方便你自己估算收益。' : '修改配置后保存即可生效。' }}</p>
      </div>

      <div v-if="globalError" class="mb-5 flex items-start justify-between gap-4 rounded-lg border border-red-100 bg-red-50 px-4 py-3 text-sm text-red-700" role="alert"><p>{{ globalError }}</p><button class="shrink-0" aria-label="关闭提示" @click="globalError = ''">✕</button></div>

      <div class="mb-6 flex flex-wrap items-start gap-3">
        <nav class="flex flex-wrap gap-2" aria-label="日常功能"><button v-for="[key,label] in tabs" :key="key" class="rounded-lg px-4 py-2.5 text-sm font-medium" :class="tab===key?'bg-blue-600 text-white':'border border-slate-200 bg-white text-slate-600'" :aria-current="tab===key?'page':undefined" @click="tab=key">{{label}}</button></nav>
        <details class="ml-auto text-sm"><summary class="cursor-pointer rounded-lg px-3 py-2.5 text-slate-500">更多设置</summary><nav class="mt-2 grid gap-1 rounded-xl border border-slate-200 bg-white p-2" aria-label="更多设置"><button v-for="[key,label] in advancedTabs" :key="key" class="rounded-lg px-4 py-2 text-left text-xs" :class="tab===key?'bg-blue-50 text-blue-700':'text-slate-500 hover:bg-slate-50'" @click="tab=key">{{label}}</button></nav></details>
      </div>
      <BusinessDashboard v-if="tab==='stats'" :api="api" />
      <RecordsPanel v-else-if="['instances','accounts','checks','events','slots','audit'].includes(tab)" :kind="tab" :api="api" />
      <InterfaceCenter v-else-if="tab==='center'" :api="api" />
      <ClientSettings v-else-if="tab==='settings'" :api="api" />
      <div v-else-if="loading" class="panel flex items-center justify-center gap-3 py-20 text-sm text-slate-500" role="status"><span class="spinner text-blue-600" aria-hidden="true"></span>正在读取配置…</div>
      <div v-else-if="loadError" class="panel p-10 text-center" role="alert"><p class="font-medium text-slate-800">配置加载失败</p><p class="mt-2 break-words text-sm text-slate-500">{{ loadError }}</p><button class="button-primary mt-5" @click="loadConfig">重新加载</button></div>

      <template v-else-if="loaded">
        <section v-show="tab === 'links'" id="panel-links" role="tabpanel" aria-labelledby="tab-links">
          <div class="panel mb-6 flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between">
            <div class="min-w-0"><div class="mb-2 flex items-center gap-2"><span class="rounded bg-emerald-50 px-2 py-0.5 font-mono text-xs font-semibold text-emerald-700">GET</span><span class="text-xs text-slate-500">网盘功能检查测试源 · 无需鉴权 · 禁用缓存</span></div><a :href="publicOrigin + '/api/check-link?provider=baidu'" class="break-all font-mono text-xs leading-6 text-blue-600 hover:underline sm:text-sm" target="_blank" rel="noopener noreferrer">{{ publicOrigin }}/api/check-link?provider=baidu</a><p class="mt-1 text-xs leading-6 text-slate-400">provider 支持 baidu、quark、xunlei；未配置或停用时返回明确错误。</p></div>
            <button class="button-secondary shrink-0 self-start sm:self-auto" @click="copyUrl('links')">{{ copied === 'links' ? '已复制' : '复制接口地址' }}</button>
          </div>

          <div class="mb-6 rounded-xl border border-blue-100 bg-blue-50/70 px-5 py-4 text-xs leading-6 text-blue-900">
            <p class="font-semibold">测试文件要求</p><p class="mt-1 text-blue-800/85">每个分享仅放无敏感内容、1 MB 以内且允许转存的小文件。相对路径须精确匹配分享内文件，客户端只转存该文件，并自行完成临时目录清理。</p><p class="mt-1 text-blue-800/85">此处只维护测试源，不登录网盘、不自动验证分享，也不执行定时检查。请先验证真实分享可转存，再启用配置。</p>
          </div>
          <div class="mb-4 flex items-center justify-between"><h3 class="text-sm font-semibold">测试源配置</h3><span class="text-xs text-slate-400">已保存启用 {{ enabledLinks }} / 3</span></div>
          <div class="grid gap-5 lg:grid-cols-3">
            <form v-for="provider in providers" :key="provider.provider" class="panel flex min-w-0 flex-col" @submit.prevent="saveLink(provider.provider)">
              <div class="flex items-center justify-between gap-3 border-b border-slate-100 px-5 py-5">
                <div class="flex items-center gap-3"><span class="flex h-10 w-10 items-center justify-center rounded-xl text-base font-semibold" :class="provider.color">{{ provider.initial }}</span><div><h4 class="text-sm font-semibold">{{ provider.name }}</h4><p class="mt-0.5 font-mono text-[11px] text-slate-400">{{ provider.provider }}</p></div></div>
                <span class="badge" :class="savedLinks[provider.provider]?.enabled ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-500'">{{ savedLinks[provider.provider]?.enabled ? '已启用' : hasExample(savedLinks[provider.provider]) ? '示例未启用' : '未启用' }}</span>
              </div>
              <fieldset class="min-w-0 space-y-4 p-5" :disabled="linkStates[provider.provider].busy">
                <p v-if="hasExample(links[provider.provider])" class="rounded-lg bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800">当前含 EXAMPLE_NOT_REAL 占位内容，不能用于真实检查。替换后方可启用。</p>
                <p class="text-xs text-slate-400 break-all">{{links[provider.provider].source_id}} · 版本 {{links[provider.provider].source_version}}</p>
                <label class="field"><span class="field-label">到期时间 · RFC3339，可留空</span><input class="input" v-model="links[provider.provider].expires_at" placeholder="2027-01-01T00:00:00+08:00"/></label>
                <label class="field"><span class="field-label">分享链接</span><input v-model="links[provider.provider].share_url" class="input" type="url" :placeholder="provider.url" autocomplete="off" spellcheck="false" :required="links[provider.provider].enabled" /></label>
                <label class="field"><span class="field-label">提取码 <span class="font-normal text-slate-400">· 没有则留空</span></span><input v-model="links[provider.provider].password" class="input" type="text" placeholder="选填" autocomplete="off" spellcheck="false" /></label>
                <label class="field"><span class="field-label">测试文件相对路径</span><input v-model="links[provider.provider].file_path" class="input" type="text" placeholder="功能检查.txt" autocomplete="off" spellcheck="false" :required="links[provider.provider].enabled" /><span class="mt-2 block text-[11px] leading-5 text-slate-400">例如：功能检查.txt 或 测试/功能检查.txt</span></label>
                <label class="flex cursor-pointer items-center gap-2.5 pt-1 text-sm text-slate-700"><input v-model="links[provider.provider].enabled" type="checkbox" />启用此测试源</label>
              </fieldset>
              <div class="mt-auto border-t border-slate-100 bg-slate-50/40 px-5 py-4"><div class="flex items-center justify-between gap-2"><span class="text-[11px]" :class="linkDirty(provider.provider) ? 'text-amber-600' : 'text-slate-400'">{{ linkDirty(provider.provider) ? '有未保存的修改' : '与已保存配置一致' }}</span><button class="button-primary !px-3.5 !py-2 !text-xs" type="submit" :disabled="linkStates[provider.provider].busy"><span v-if="linkStates[provider.provider].busy" class="spinner" aria-hidden="true"></span>{{ linkStates[provider.provider].busy ? '保存中…' : '保存配置' }}</button></div><p v-if="linkStates[provider.provider].message" class="mt-3 break-words text-xs leading-5" :class="linkStates[provider.provider].error ? 'status-error' : 'status-ok'" role="status">{{ linkStates[provider.provider].message }}</p></div>
            </form>
          </div>
        </section>

        <section v-show="tab === 'sites'" id="panel-sites" role="tabpanel" aria-labelledby="tab-sites">
          <div class="panel mb-6 flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between">
            <div class="min-w-0"><div class="mb-2 flex items-center gap-2"><span class="rounded bg-emerald-50 px-2 py-0.5 font-mono text-xs font-semibold text-emerald-700">GET</span><span class="text-xs text-slate-500">搜索站点配置 · 无需鉴权</span></div><a :href="publicOrigin + '/api/search-sites'" class="break-all font-mono text-xs leading-6 text-blue-600 hover:underline sm:text-sm" target="_blank" rel="noopener noreferrer">{{ publicOrigin }}/api/search-sites</a><p class="mt-1 text-xs leading-6 text-slate-400">返回真实配置及 enabled 状态，客户端按 order 排序并识别启停。</p></div>
            <button class="button-secondary shrink-0 self-start sm:self-auto" @click="copyUrl('sites')">{{ copied === 'sites' ? '已复制' : '复制接口地址' }}</button>
          </div>
          <div class="mb-6 rounded-xl border border-blue-100 bg-blue-50/70 px-5 py-4 text-xs leading-6 text-blue-800">此接口提供站点入口配置。首页网址不代表可直接调用的搜索 API；实际资源搜索需要对应的搜索接口或插件协议。站点标识创建后固定，修改名称或网址不会改变标识。</div>

          <div class="mb-4 flex flex-wrap items-center justify-between gap-3"><div><h3 class="text-sm font-semibold">站点列表 <span class="ml-2 text-xs font-normal text-slate-400">{{ sites.length }} 个站点 · 已保存启用 {{ enabledSites }} 个</span></h3></div><div class="flex w-full gap-2 sm:w-auto"><label class="min-w-0 flex-1 sm:w-60"><span class="sr-only">搜索站点</span><input v-model="search" class="input !py-2" type="search" placeholder="搜索名称、标识或网址" /></label><button class="button-primary shrink-0 !py-2" :disabled="showNewSite" @click="openNewSite"><span aria-hidden="true">＋</span>新增站点</button></div></div>

          <form v-if="showNewSite" class="panel mb-5 border-blue-200 p-5" @submit.prevent="addSite">
            <div class="mb-5 flex items-center justify-between"><h4 class="text-sm font-semibold text-blue-700">新增搜索站点</h4><button type="button" class="text-xs text-slate-500 hover:text-slate-800" :disabled="newSiteState.busy" @click="showNewSite = false">取消</button></div>
            <fieldset class="grid min-w-0 gap-4 sm:grid-cols-2 lg:grid-cols-4" :disabled="newSiteState.busy">
              <label class="field"><span class="field-label">稳定标识 <span class="font-normal text-slate-400">· 创建后不可更改</span></span><input v-model="newSite.id" class="input" required maxlength="100" placeholder="字母、数字、_ 或 -" autocomplete="off" spellcheck="false" /></label>
              <label class="field"><span class="field-label">站点名称</span><input v-model="newSite.name" class="input" required maxlength="200" placeholder="输入实际站点名称" /></label>
              <label class="field sm:col-span-2 lg:col-span-1"><span class="field-label">入口网址</span><input v-model="newSite.url" class="input" type="url" required placeholder="完整 HTTP / HTTPS 地址" autocomplete="off" spellcheck="false" /></label>
              <div class="flex min-w-0 items-end gap-5"><label class="field w-28"><span class="field-label">排序 <span class="font-normal text-slate-400">· 小值优先</span></span><input v-model="newSite.order" class="input" required type="number" min="0" step="1" /></label><label class="mb-3 flex cursor-pointer items-center gap-2 text-sm"><input v-model="newSite.enabled" type="checkbox" />启用</label></div>
            </fieldset>
            <div class="mt-5 flex flex-wrap items-center justify-between gap-3"><p class="text-xs text-slate-400">仅填写实际使用的站点，不添加示例数据。</p><button class="button-primary !py-2" :disabled="newSiteState.busy" type="submit">{{ newSiteState.busy ? '保存中…' : '保存新站点' }}</button></div><p v-if="newSiteState.message" class="mt-3 text-xs leading-5" :class="newSiteState.error ? 'status-error' : 'status-ok'" role="status">{{ newSiteState.message }}</p>
          </form>

          <div class="panel">
            <div v-if="visibleSites.length === 0" class="px-5 py-14 text-center"><p class="text-sm font-medium text-slate-600">{{ sites.length ? '没有匹配的站点' : '尚未配置搜索站点' }}</p><p class="mt-2 text-xs text-slate-400">{{ sites.length ? '试试其他名称、标识或网址。' : '点击“新增站点”，添加真实的站点入口。' }}</p></div>
            <div v-else>
              <div class="hidden grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)_minmax(0,1.8fr)_76px_60px_80px] gap-4 border-b border-slate-100 bg-slate-50/70 px-5 py-3 text-xs font-medium text-slate-500 lg:grid"><span>稳定标识</span><span>站点名称</span><span>入口网址</span><span>排序</span><span>启用</span><span class="text-right">操作</span></div>
              <form v-for="site in visibleSites" :key="site.id" class="border-b border-slate-100 p-5 last:border-b-0" @submit.prevent="saveSite(site)">
                <fieldset class="grid min-w-0 grid-cols-2 gap-4 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)_minmax(0,1.8fr)_76px_60px_80px] lg:items-center" :disabled="siteStates[site.id].busy">
                  <label class="field"><span class="field-label lg:sr-only">稳定标识（只读）</span><input :value="site.id" class="input !text-xs" readonly :aria-label="site.name + '的稳定标识，不可更改'" /></label>
                  <label class="field"><span class="field-label lg:sr-only">站点名称</span><input v-model="site.name" class="input" required maxlength="200" :aria-label="site.id + '的站点名称'" /></label>
                  <label class="field col-span-2 lg:col-span-1"><span class="field-label lg:sr-only">入口网址</span><input v-model="site.url" class="input !text-xs" required type="url" autocomplete="off" spellcheck="false" :aria-label="site.id + '的入口网址'" /></label>
                  <label class="field"><span class="field-label lg:sr-only">排序（小值优先）</span><input v-model="site.order" class="input" required type="number" min="0" step="1" :aria-label="site.id + '的排序值'" /></label>
                  <label class="flex cursor-pointer items-center gap-2.5 self-end py-3 text-sm lg:self-auto lg:justify-center"><input v-model="site.enabled" type="checkbox" :aria-label="'启用' + site.id" /><span class="lg:sr-only">启用</span></label>
                  <button class="button-secondary col-span-2 !py-2 !text-xs lg:col-span-1" type="submit" :disabled="siteStates[site.id].busy">{{ siteStates[site.id].busy ? '保存中…' : '保存' }}</button>
                </fieldset>
                <button type="button" class="mt-3 text-xs text-red-600" @click="deleteSite(site)">移除站点</button>
                <div v-if="siteDirty(site.id) || siteStates[site.id].message" class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs leading-5"><span v-if="siteDirty(site.id)" class="text-amber-600">有未保存的修改</span><span v-if="siteStates[site.id].message" :class="siteStates[site.id].error ? 'status-error' : 'status-ok'" role="status">{{ siteStates[site.id].message }}</span></div>
              </form>
            </div>
          </div>
          <p class="mt-4 text-xs leading-6 text-slate-400">每行修改后点击“保存”。停用站点会保留其稳定标识和配置，公共接口通过 enabled 字段表达状态。</p>
        </section>
      </template>
      <footer class="mt-10 flex flex-wrap justify-between gap-2 border-t border-slate-200 pt-5 text-[11px] leading-5 text-slate-400"><span>小白搜盘 · 管理中心服务</span><span>使用数据来自客户端上报</span></footer>
    </main>
  </div>
</template>
