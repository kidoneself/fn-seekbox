<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
const props = defineProps({ api: { type: Function, required: true } })
const info = ref(null), error = ref(''), busy = ref(false)
const names = { baidu: '百度网盘', quark: '夸克网盘', uc: 'UC 网盘', xunlei: '迅雷云盘' }
let timer, alive = true
async function refresh() {
  if (busy.value) return
  busy.value = true
  try { const value = await props.api('/scan/status'); if (alive) { info.value = value; error.value = '' } }
  catch (e) { if (alive) error.value = e.message }
  finally { busy.value = false }
}
onMounted(() => { refresh(); timer = setInterval(refresh, 10000) })
onUnmounted(() => { alive = false; clearInterval(timer) })
const mib = value => Math.round((value || 0) / 1048576)
</script>
<template>
  <section class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-slate-500">迅雷最多 {{ info?.limits?.perProvider?.xunlei ?? '—' }} 个账号同时扫码，使用 {{ info?.browser_pool?.max_browsers ?? '—' }} 个共享浏览器，各账号登录环境独立。</p>
      <button class="rounded-lg border border-slate-300 bg-white px-4 py-2 text-sm" :disabled="busy" @click="refresh">{{ busy ? '刷新中…' : '刷新' }}</button>
    </div>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
    <template v-if="info">
      <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div v-for="item in [['正在登录', info.active], ['排队中', info.queued], ['保留会话', info.sessions], ['容器内存', info.container_memory_bytes == null ? '—' : mib(info.container_memory_bytes) + ' MB']]" :key="item[0]" class="rounded-xl border border-slate-200 bg-white p-4">
          <p class="text-sm text-slate-500">{{ item[0] }}</p><p class="mt-2 text-2xl font-semibold text-slate-900">{{ item[1] }}</p>
        </div>
      </div>
      <div class="overflow-x-auto rounded-xl border border-slate-200 bg-white">
        <table class="w-full whitespace-nowrap text-left text-sm">
          <thead class="border-b bg-slate-50 text-slate-600"><tr><th class="p-4">网盘</th><th class="p-4">并发 / 上限</th><th class="p-4">排队</th><th class="p-4">已取得凭据</th><th class="p-4">NAS 确认保存</th><th class="p-4">失败 / 过期</th><th class="p-4">限流拒绝</th></tr></thead>
          <tbody><tr v-for="row in info.providers" :key="row.provider" class="border-b last:border-0"><td class="p-4 font-medium">{{ names[row.provider] }}</td><td class="p-4">{{ row.active }} / {{ row.limit }}</td><td class="p-4">{{ row.queued }}</td><td class="p-4">{{ row.ready }}</td><td class="p-4 text-emerald-700">{{ row.acknowledged }}</td><td class="p-4">{{ row.failed }} / {{ row.expired }}</td><td class="p-4">{{ row.rejected }}</td></tr></tbody>
        </table>
      </div>
      <div class="rounded-xl border border-slate-200 bg-white p-4 text-sm leading-7 text-slate-600">
        <p>最多排队 {{ info.limits.queued }} 个会话，等待超过 {{ info.limits.queueMs / 1000 }} 秒后提示重试；轮到时才生成二维码。</p>
        <p>每个实例最多保留 {{ info.limits.perClient }} 个会话，同时执行 {{ info.limits.perClientActive }} 个。取消、过期后关闭该账号的登录环境，不影响其他账号。</p>
        <p v-if="info.browser_pool">浏览器 {{ info.browser_pool.browser_processes }} / {{ info.browser_pool.max_browsers }}，每个最多 {{ info.browser_pool.contexts_per_browser }} 个独立登录环境；空闲 {{ info.browser_pool.idle_seconds }} 秒后退出。</p>
        <p>统计从本次启动起累计（{{ new Date(info.started_at).toLocaleString('zh-CN') }}），服务重启后归零。“NAS 确认保存”仅在客户端确认后增加。</p>
        <p>容器内存包含登录浏览器；部署内存上限 2 GB，CPU 上限 1.5 核。Cookie 和 Token 不在此展示或长期保存。</p>
      </div>
    </template>
  </section>
</template>
