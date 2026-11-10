<script setup>
import {ref,computed,onMounted} from 'vue'
const props=defineProps({api:Function})
const range=ref('today'),overview=ref(null),history=ref([]),busy=ref(false),error=ref(''),loadedRange=ref('today')
const ranges=[['today','今天'],['week','近7天'],['month','本月']]
const providers=[['baidu','百度网盘'],['quark','夸克网盘'],['xunlei','迅雷云盘']]
const period=computed(()=>ranges.find(([key])=>key===loadedRange.value)?.[1]||'今天')
const cards=computed(()=>[
 ['registered_instances','累计使用人数','人','按安装数量估算'],
 ['online_instances','现在还在用','人','最近30分钟有运行心跳'],
 ['logged_in_instances','已登录网盘人数','人','至少一个网盘最近确认登录'],
 ['valid_accounts','已登录网盘账号','个','一个人可以登录多个账号'],
 ['executed_runs',period.value+'执行检查','次','包含失败、跳过和未完成'],
 ['successful_cycles',period.value+'完整成功','次','转存和删除均已确认成功'],
])
function dates(){const date=new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());const midnight=new Date(date+'T00:00:00+08:00');return {date,midnight,to:new Date(+midnight+86400000).toISOString(),week:new Date(+midnight-6*86400000).toISOString()}}
function params(from,to){return new URLSearchParams({from,to})}
async function load(){if(busy.value)return;busy.value=true;error.value='';const selected=range.value;try{const d=dates();const from=selected==='week'?d.week:selected==='month'?d.date.slice(0,8)+'01T00:00:00+08:00':d.midnight.toISOString();const q=params(from,d.to);const tq=params(selected==='today'?d.week:from,d.to);tq.set('metric','successful_cycles');tq.set('group_by','provider');const [stats,trend]=await Promise.all([props.api('/analytics/overview?'+q),props.api('/analytics/trends?'+tq)]);const byDay=new Map();for(const row of trend.items){if(!byDay.has(row.bucket))byDay.set(row.bucket,{date:row.bucket,baidu:0,quark:0,xunlei:0,total:0});const item=byDay.get(row.bucket);if(providers.some(([key])=>key===row.group)){item[row.group]=row.value;item.total+=row.value}}overview.value=stats;history.value=[...byDay.values()].sort((a,b)=>b.date.localeCompare(a.date));loadedRange.value=selected}catch(e){error.value=e.message}finally{busy.value=false}}
function download(){const rows=[['日期（北京时间）','百度成功次数','夸克成功次数','迅雷成功次数','合计成功次数'],...history.value.map(x=>[x.date,x.baidu,x.quark,x.xunlei,x.total])];const url=URL.createObjectURL(new Blob(['\uFEFF'+rows.map(x=>x.join(',')).join('\r\n')],{type:'text/csv;charset=utf-8'}));const a=document.createElement('a');a.href=url;a.download='每日网盘检查成功次数.csv';a.click();URL.revokeObjectURL(url)}
onMounted(load)
</script>
<template>
<section class="space-y-5" aria-label="使用数据">
 <div class="flex flex-wrap items-center justify-between gap-3"><div class="flex rounded-xl border border-slate-200 bg-white p-1" role="group" aria-label="统计日期"><button v-for="[key,label] in ranges" :key="key" class="rounded-lg px-4 py-2 text-sm" :class="range===key?'bg-blue-50 font-medium text-blue-700':'text-slate-500'" :aria-pressed="range===key" :disabled="busy" @click="range=key;load()">{{label}}</button></div><button class="button-secondary" :disabled="busy" @click="load">{{busy?'读取中…':'刷新数据'}}</button></div>
 <p v-if="error" class="status-error text-sm" role="alert">{{error}}<span v-if="overview">（下方保留上次成功读取的数据）</span></p>
 <p v-if="!overview&&!error" class="panel p-8 text-center text-sm text-slate-500" role="status">正在读取实际使用数据…</p>
 <template v-if="overview">
  <div v-if="overview.registered_instances===0" class="rounded-xl border border-blue-100 bg-blue-50 p-4 text-sm leading-6 text-blue-900"><strong>还没有客户端上报使用数据。</strong><p>小白搜盘接入上报后，这里才会显示人数和检查次数。现在的 0 不代表没人安装。</p></div>
  <div class="grid grid-cols-2 gap-3 lg:grid-cols-3"><div v-for="[key,label,unit,hint] in cards" :key="key" class="panel p-4 sm:p-5" :class="key==='successful_cycles'?'!border-blue-200 bg-blue-50/30':''"><h3 class="text-xs font-medium text-slate-600 sm:text-sm">{{label}}</h3><p class="mt-3 text-3xl font-semibold tabular-nums sm:text-4xl" :class="key==='successful_cycles'?'text-blue-600':'text-slate-900'">{{overview[key]}}<span class="ml-1 text-xs font-normal text-slate-400">{{unit}}</span></p><p class="mt-3 text-[11px] leading-5 text-slate-400">{{hint}}</p></div></div>
  <p class="text-xs leading-6 text-slate-500">人数按安装数量估算，同一人重复安装可能重复计算。人数和登录状态看当前数据，日期只影响检查次数。</p>
  <section class="panel p-4 sm:p-6"><h3 class="font-semibold text-slate-900">分别看三个网盘</h3><p class="mt-1 text-xs leading-6 text-slate-500">{{period}}自动检查；完整成功 1 次，就是转存和删除都确认完成 1 次。</p><div class="mt-5 grid gap-5 sm:grid-cols-3"><div v-for="[key,label] in providers" :key="key" class="rounded-xl bg-slate-50 p-4"><h4 class="mb-4 text-sm font-semibold">{{label}}</h4><dl class="space-y-3 text-xs"><div class="flex justify-between gap-2"><dt class="text-slate-500">已登录账号</dt><dd class="font-medium tabular-nums">{{overview.valid_accounts_by_provider[key]}} 个</dd></div><div class="flex justify-between gap-2"><dt class="text-slate-500">{{period}}执行检查</dt><dd class="font-medium tabular-nums">{{overview.executed_runs_by_provider[key]}} 次</dd></div><div class="flex justify-between gap-2"><dt class="text-slate-500">{{period}}完整成功</dt><dd class="font-semibold tabular-nums text-blue-600">{{overview.successful_cycles_by_provider[key]}} 次</dd></div></dl></div></div></section>
  <section class="panel p-4 sm:p-6"><div class="flex flex-wrap items-center justify-between gap-3"><div><h3 class="font-semibold text-slate-900">每天成功检查多少次</h3><p class="mt-1 text-xs leading-6 text-slate-500">{{loadedRange==='month'?'本月':'最近7天'}}，按网盘列出，方便你用自己的规则估算收益。</p></div><button class="button-secondary !text-xs" @click="download">下载每日数据</button></div><div class="mt-4 overflow-x-auto"><table class="w-full text-right text-xs sm:text-sm"><thead class="border-b border-slate-100 text-slate-500"><tr><th class="py-3 pr-3 text-left font-medium">日期</th><th class="px-2 font-medium">百度</th><th class="px-2 font-medium">夸克</th><th class="px-2 font-medium">迅雷</th><th class="pl-2 font-medium">合计</th></tr></thead><tbody><tr v-for="row in history" :key="row.date" class="border-b border-slate-100 last:border-0"><td class="whitespace-nowrap py-3 pr-3 text-left text-slate-500">{{row.date}}</td><td class="px-2 tabular-nums">{{row.baidu}}</td><td class="px-2 tabular-nums">{{row.quark}}</td><td class="px-2 tabular-nums">{{row.xunlei}}</td><td class="pl-2 font-semibold tabular-nums">{{row.total}}</td></tr></tbody></table></div></section>
  <details class="text-xs leading-6 text-slate-500"><summary class="cursor-pointer">这些数字怎么算？</summary><div class="mt-2 space-y-1"><p>“已登录”是最近26小时内确认有效的网盘登录。失效、未验证和已移除账号不计入；同一安装登录多个账号，登录人数只算一次。</p><p>检查次数包括自动每日检查和补检，手动测试单独保留在详细记录里。检查按执行日期统计，成功按删除确认日期统计，所以补清理可能在第二天才算成功。</p><p>当前日期全部按北京时间。上报数据用于了解使用情况，实际收益需要你按合作方的规则另行核算。</p></div></details>
 </template>
</section>
</template>
