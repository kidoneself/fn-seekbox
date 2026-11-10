<script setup>
import {ref,onMounted} from 'vue'
const props=defineProps({api:Function});const form=ref(null),error=ref(''),notice=ref(''),busy=ref(false)
const fields=[['heartbeat_interval_seconds','心跳间隔（秒）',60,1800],['event_batch_max_items','每批事件条数',1,100],['event_batch_max_bytes','每批字节数',16384,262144],['event_max_age_days','补报窗口（天）',1,7],['raw_retention_days','原始明细保留（天）',30,365],['summary_retention_days','每日汇总保留（天）',365,730]]
async function load(){try{form.value=await props.api('/client-settings')}catch(e){error.value=e.message}}
async function save(){busy.value=true;error.value='';try{const body=Object.fromEntries(fields.map(([key])=>[key,Number(form.value[key])]));form.value=await props.api('/client-settings',{method:'PATCH',body:JSON.stringify(body)});notice.value='已保存。客户端下次获取配置时生效。'}catch(e){error.value=e.message}finally{busy.value=false}}
onMounted(load)
</script>
<template><form class="panel max-w-3xl p-6" @submit.prevent="save"><h3 class="font-semibold">上报与保留策略</h3><p class="mb-6 mt-2 text-xs text-slate-500">成功次数台账长期保留，清理原始明细不会清零累计次数。</p><p v-if="error" class="status-error mb-4 text-sm" role="alert">{{error}}</p><div v-if="form" class="grid gap-5 sm:grid-cols-2"><label v-for="[key,label,min,max] in fields"><span class="field-label">{{label}}</span><input class="input" type="number" :min="min" :max="max" step="1" v-model="form[key]" required/><small class="text-slate-400">{{min}} – {{max}}</small></label></div><p v-if="form" class="my-5 text-xs text-slate-400">配置版本 {{form.config_version}}</p><button class="button-primary" :disabled="busy||!form">保存设置</button><p v-if="notice" class="status-ok mt-3 text-sm" role="status">{{notice}}</p></form></template>
