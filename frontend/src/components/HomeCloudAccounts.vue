<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import SupportProject from '@/components/SupportProject.vue';
import { providerLogos } from '@/utils/providerLogos';
import { cloudError, listCloudAccounts, type CloudAccount, type CloudProvider } from '@/api/cloud';
const props = defineProps<{ revision: number }>();
const emit = defineEmits<{ configure: [provider: CloudProvider]; records: [] }>();
const providers = [ { id: 'baidu', name: '百度网盘' }, { id: 'quark', name: '夸克网盘' }, { id: 'xunlei', name: '迅雷云盘' } ] as const;
const accounts = ref<CloudAccount[]>([]);
const loading = ref(true);
const error = ref('');
let request = 0;
const load = async () => {
  const current = ++request; loading.value = true; error.value = '';
  try { const data = await listCloudAccounts(); if (current === request) accounts.value = data; }
  catch (err) { if (current === request) error.value = cloudError(err); }
  finally { if (current === request) loading.value = false; }
};
const connected = (id: CloudProvider) => accounts.value.filter(a => a.provider === id && a.status === 'ready').length;
const status = (id: CloudProvider) => loading.value ? '读取状态中…' : error.value ? '状态暂不可用' : connected(id) ? `已连接 ${connected(id)} 个账号` : accounts.value.some(a => a.provider === id) ? '需要重新登录' : '未登录';
onMounted(load);
watch(() => props.revision, load);
</script>
<template>
  <section class="home-cloud" aria-label="网盘账号配置">
    <div class="account-panel"><header><div><h2>我的网盘</h2><p>登录网盘后，可转存搜索结果或添加追更。</p></div><button type="button" @click="emit('records')">我的追更 <span aria-hidden="true">→</span></button></header>
    <p v-if="error" class="load-error" role="alert">{{ error }} <button type="button" @click="load">重试</button></p>
    <div class="provider-grid">
      <article v-for="item in providers" :key="item.id" class="provider-card">
        <div class="provider-top"><img class="provider-mark" :src="providerLogos[item.id]" :alt="item.name + ' Logo'" width="36" height="36"><div><h3>{{ item.name }}</h3><p :class="{ connected: connected(item.id) }">{{ status(item.id) }}</p></div></div>
        <button type="button" class="connect" :aria-label="item.name + '账号设置'" @click="emit('configure', item.id)">{{ connected(item.id) ? '管理账号' : '扫码登录' }} <span aria-hidden="true">↗</span></button>
      </article>
    </div>
    </div>
    <SupportProject />
  </section>
</template>
<style scoped>
.home-cloud { width: 100%; margin: 0; text-align: left; display: flex; flex-direction: column; gap: 20px; min-height: 100%; }
.account-panel { padding: 20px 24px; border: 1px solid hsl(var(--border)); border-radius: 8px; background: hsl(var(--card)); }
header { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 18px; }
h2,h3,p { margin: 0; } h2 { font-size: 15px; font-weight: 650; } header p { color: hsl(var(--muted-foreground)); font-size: 12px; margin-top: 5px; line-height: 1.5; }
button { cursor: pointer; white-space: nowrap; } header button,.load-error button { border: 0; background: none; color: hsl(var(--primary)); font-size: 12px; }
.provider-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 16px; }
.provider-card { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px; border: 1px solid hsl(var(--border)); border-radius: 6px; background: #fafbfc; }
.provider-top { display: flex; align-items: center; gap: 10px; } .provider-mark { width: 34px; height: 34px; object-fit: contain; flex-shrink: 0; }
h3 { font-size: 13px; font-weight: 600; } .provider-top p { font-size: 11px; color: hsl(var(--muted-foreground)); margin-top: 5px; } .provider-top p.connected { color: hsl(var(--primary)); }
.connect { display: flex; align-items: center; gap: 8px; padding: 7px 10px; border: 1px solid hsl(var(--border)); border-radius: 5px; color: hsl(var(--primary)); background: white; font-size: 12px; }
.connect:hover { border-color: hsl(var(--primary)); background: hsl(var(--primary) / .04); } .load-error { font-size: 12px; color: #b45309; margin-bottom: 10px; }
button:focus-visible { outline: 2px solid hsl(var(--primary)); outline-offset: 3px; }
@media(min-width:761px) and (max-width:1100px) { .account-panel { padding: 16px; }.provider-grid { gap: 10px; }.provider-card { padding: 10px; gap: 6px; }.provider-top { gap: 6px; }.provider-mark { width: 28px; height: 28px; }.connect { padding: 6px; font-size: 11px; gap: 3px; } }
@media(min-width:761px) and (max-height:650px) { .account-panel { padding: 14px 16px; } header { margin-bottom: 12px; } header > div { display: flex; align-items: baseline; gap: 12px; } header p { margin: 0; font-size: 11px; }.provider-card { padding-block: 10px; } }
@media(max-width:760px) { .home-cloud { gap: 14px; }.account-panel { padding: 14px; }header { align-items: start; margin-bottom: 14px; }header p { font-size: 11px; }.provider-grid { grid-template-columns: 1fr; gap: 8px; }.provider-card { padding: 12px; } }
</style>
