<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { cloudJobActive, cloudTime, type CloudSubscription, type CloudJob, type CloudAccount, type CloudProvider } from '@/api/cloud';
import { providerLogos } from '@/utils/providerLogos';
import { getDiskTypeName } from '@/utils/diskTypes';
import CloudExecutionLog from './CloudExecutionLog.vue';
import CloudProgressDetail from './CloudProgressDetail.vue';
const props = defineProps<{ subscriptions: CloudSubscription[]; jobs: CloudJob[]; accounts: CloudAccount[]; busy: boolean; records?: boolean }>();
const emit = defineEmits<{
  check: [id: string]; toggle: [item: CloudSubscription]; remove: [id: string];
  update: [id: string, patch: { interval_minutes?: number; filter_ads?: boolean }];
}>();
interface Plan { id: string; title: string; provider: CloudProvider; accountID: string; subscription?: CloudSubscription; jobs: CloudJob[] }
const expanded = ref(new Set<string>());
const settings = ref(new Set<string>());
const removing = ref('');
const intervals = ref<Record<string, number>>({});
const query = ref('');
watch(() => props.subscriptions, list => { for (const item of list) if (!(item.id in intervals.value)) intervals.value[item.id] = item.interval_minutes; }, { immediate: true });
const accountLabel = (id: string) => props.accounts.find(a => a.id === id)?.name || '账号已移除';
const plans = computed(() => {
  const map = new Map<string, Plan>();
  for (const item of props.subscriptions) map.set(item.id, { id: item.id, title: item.title || '分享资源', provider: item.provider, accountID: item.account_id, subscription: item, jobs: [] });
  for (const job of props.jobs) {
    if (!job.subscription_id) continue;
    let plan = map.get(job.subscription_id);
    if (!plan && props.records) { plan = { id: job.subscription_id, title: job.title || '分享资源', provider: job.provider, accountID: job.account_id, jobs: [] }; map.set(plan.id, plan); }
    plan?.jobs.push(job);
  }
  return [...map.values()];
});
const filtered = computed(() => plans.value.filter(p => `${p.title} ${accountLabel(p.accountID)} ${getDiskTypeName(p.provider)}`.toLowerCase().includes(query.value.trim().toLowerCase())));
const oneTime = computed(() => props.records ? props.jobs.filter(j => !j.subscription_id) : []);
const active = (p: Plan) => p.jobs.some(cloudJobActive) || p.subscription?.status === 'running';
const progress = (p: Plan) => p.subscription?.progress || [...p.jobs].filter(j => j.progress).sort((a,b) => b.created_at-a.created_at)[0]?.progress;
const status = (p: Plan) => !p.subscription ? '计划已移除' : active(p) ? '正在检查' : !p.subscription.enabled ? '已暂停' : p.subscription.last_error ? '需要处理' : '追更中';
const toggleSet = (kind: 'plan' | 'settings', id: string) => { const value = kind === 'plan' ? expanded : settings; const next = new Set(value.value); next.has(id) ? next.delete(id) : next.add(id); value.value = next; };
const check = (id: string) => { expanded.value = new Set([...expanded.value, id]); emit('check', id); };
const validInterval = (id: string) => Number.isInteger(intervals.value[id]) && intervals.value[id] >= 15 && intervals.value[id] <= 1440;
</script>

<template>
  <section class="plan-list" aria-label="追更计划">
    <div class="plan-toolbar"><span><strong>{{ plans.length }}</strong> 个追更计划 <span class="toolbar-separator">·</span> {{ subscriptions.filter(s => s.enabled).length }} 个启用</span><input v-if="plans.length" v-model="query" type="search" placeholder="搜索计划、网盘或账号" aria-label="搜索追更计划"></div>
    <p v-if="!plans.length" class="plan-empty">还没有追更计划。在搜索结果中点击「追更」即可添加。</p>
    <p v-else-if="!filtered.length" class="plan-empty">没有匹配的追更计划。</p>
    <div v-else class="plan-rows">
      <article v-for="plan in filtered" :key="plan.id" class="plan" :class="{ expanded: expanded.has(plan.id) }">
        <div class="plan-heading">
          <button class="plan-toggle" type="button" :aria-expanded="expanded.has(plan.id)" :aria-controls="'plan-' + plan.id" @click="toggleSet('plan', plan.id)">
            <svg class="chevron" :class="{ open: expanded.has(plan.id) }" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m9 5 7 7-7 7"/></svg>
            <img :src="providerLogos[plan.provider]" :alt="getDiskTypeName(plan.provider)" width="30" height="30">
            <span class="plan-name"><strong>{{ plan.title }}</strong><small>{{ accountLabel(plan.accountID) }} · {{ plan.subscription ? '每 ' + plan.subscription.interval_minutes + ' 分钟检查' : '保留执行记录' }}</small></span>
            <span class="plan-progress"><strong>分享 {{ progress(plan)?.source.label || (progress(plan) ? '未识别' : '待检查') }}</strong><small>已保存 {{ progress(plan)?.saved.label || '—' }}</small></span>
            <span class="plan-state" :class="{ bad: !!plan.subscription?.last_error, live: active(plan) || (plan.subscription?.enabled && !plan.subscription?.last_error) }">{{ status(plan) }}</span>
            <span class="plan-metric"><strong>{{ plan.subscription ? plan.subscription.transferred : '—' }}</strong><small>累计保存</small></span>
            <span class="plan-schedule"><strong>{{ plan.subscription?.enabled ? cloudTime(plan.subscription.next_check_at) : '—' }}</strong><small>下次检查</small></span>
            <span class="plan-record-count">{{ plan.jobs.length }} 次记录<span>{{ expanded.has(plan.id) ? '收起' : '展开' }}</span></span>
          </button>
        </div>
        <div v-if="expanded.has(plan.id)" :id="'plan-' + plan.id" class="plan-content">
          <div v-if="plan.subscription" class="plan-context"><span :title="plan.subscription.destination_name || '/'">保存到 <strong>{{ plan.subscription.destination_name || '/' }}</strong></span><span>最近检查 {{ cloudTime(plan.subscription.last_checked_at) }}</span></div>
          <CloudProgressDetail :progress="progress(plan)" />
          <div class="plan-controls"><strong>执行记录</strong><div v-if="plan.subscription" class="plan-actions"><button type="button" class="primary" :disabled="busy || active(plan) || !plan.subscription.enabled" @click="check(plan.id)">{{ active(plan) ? '正在检查…' : '立即检查' }}</button><button type="button" :disabled="busy" @click="emit('toggle', plan.subscription)">{{ plan.subscription.enabled ? '暂停' : '恢复追更' }}</button><button type="button" :aria-expanded="settings.has(plan.id)" :aria-controls="'settings-' + plan.id" @click="toggleSet('settings', plan.id)">计划设置</button></div></div>
          <div v-if="plan.subscription && settings.has(plan.id)" :id="'settings-' + plan.id" class="plan-settings">
            <div class="settings-line"><label :for="'interval-' + plan.id">检查间隔</label><input :id="'interval-' + plan.id" v-model.number="intervals[plan.id]" type="number" min="15" max="1440" step="1" :disabled="busy"><span>分钟</span><button type="button" :disabled="busy || !validInterval(plan.id) || intervals[plan.id] === plan.subscription.interval_minutes" @click="emit('update', plan.id, { interval_minutes: intervals[plan.id] })">保存</button><span class="setting-hint">15–1440 分钟</span></div>
            <div class="settings-line"><label class="filter-label"><input type="checkbox" :checked="!!plan.subscription.filter_ads" :disabled="busy || active(plan)" @change="emit('update', plan.id, { filter_ads: !plan.subscription.filter_ads })">过滤广告附件</label><a :href="plan.subscription.share_url" target="_blank" rel="noopener noreferrer">查看原分享 ↗</a><button class="danger remove-plan" type="button" :disabled="busy" @click="removing = plan.id">移除计划</button></div>
            <div v-if="removing === plan.id" class="remove-confirm"><span>移除后停止追更，网盘中已保存的文件会保留。</span><button class="danger" type="button" :disabled="busy" @click="emit('remove', plan.id)">确认移除</button><button type="button" :disabled="busy" @click="removing = ''">取消</button></div>
          </div>
          <p v-if="plan.subscription?.last_error" class="plan-error">最近一次检查：{{ plan.subscription.last_error }}</p>
          <CloudExecutionLog :jobs="plan.jobs" />
        </div>
      </article>
    </div>
    <template v-if="records && oneTime.length"><h3 class="one-time-heading">一次转存 <span>{{ oneTime.length }} 条记录</span></h3><div class="standalone-log"><CloudExecutionLog :jobs="oneTime" :accounts="accounts" standalone /></div></template>
  </section>
</template>

<style scoped>
.plan-list{min-width:0;font-size:13px;color:hsl(var(--foreground))}.plan-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px;color:hsl(var(--muted-foreground));font-size:12px}.plan-toolbar strong{font-size:15px;color:hsl(var(--foreground))}.toolbar-separator{margin:0 8px;color:hsl(var(--border))}.plan-toolbar input{width:230px;padding:8px 10px;border:1px solid hsl(var(--border));border-radius:6px;background:hsl(var(--card));font:inherit;color:inherit}.plan-rows,.standalone-log{border:1px solid hsl(var(--border));border-radius:8px;background:hsl(var(--card));overflow:hidden}.plan+.plan{border-top:1px solid hsl(var(--border))}.plan-toggle{width:100%;display:flex;align-items:center;gap:14px;padding:17px 18px;border:0;background:transparent;color:inherit;text-align:left;font:inherit;cursor:pointer}.plan-toggle:hover{background:hsl(var(--muted)/.5)}.plan.expanded .plan-heading{background:hsl(var(--muted)/.3)}.chevron{flex-shrink:0;color:hsl(var(--muted-foreground));transition:transform .15s}.chevron.open{transform:rotate(90deg)}.plan-toggle img{flex-shrink:0;object-fit:contain}.plan-name{flex:1;min-width:0}.plan-name strong{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:14px;font-weight:600}.plan-name small,.plan-metric small,.plan-schedule small{display:block;margin-top:5px;font-size:11px;font-weight:400;color:hsl(var(--muted-foreground))}.plan-name small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.plan-state{font-size:11px;min-width:66px;color:hsl(var(--muted-foreground))}.plan-state:before{content:'';display:inline-block;width:5px;height:5px;border-radius:50%;background:currentColor;vertical-align:2px;margin-right:5px}.plan-state.live{color:hsl(var(--primary))}.plan-state.bad{color:hsl(var(--destructive))}.plan-metric{width:75px;text-align:right;font-variant-numeric:tabular-nums}.plan-metric strong{font-size:17px;font-weight:600}.plan-schedule{width:118px;text-align:right;font-variant-numeric:tabular-nums}.plan-schedule strong{font-weight:500;font-size:12px}.plan-record-count{width:72px;text-align:right;color:hsl(var(--muted-foreground));font-size:11px}.plan-record-count span{display:block;margin-top:5px;color:hsl(var(--primary))}.plan-content{border-top:1px solid hsl(var(--border))}.plan-context{display:flex;gap:12px 28px;flex-wrap:wrap;padding:12px 20px 0;color:hsl(var(--muted-foreground));font-size:11px}.plan-context span{min-width:0;overflow-wrap:anywhere}.plan-context strong{font-weight:500;color:hsl(var(--foreground))}.plan-controls{padding:12px 20px;display:flex;align-items:center;justify-content:space-between;gap:12px}.plan-controls>strong{font-size:12px;font-weight:600}.plan-actions{display:flex;gap:7px;flex-wrap:wrap}.plan-actions button,.plan-settings button{padding:6px 10px;border:1px solid hsl(var(--border));border-radius:5px;background:hsl(var(--card));color:inherit;font:inherit;font-size:12px;cursor:pointer}.plan-actions .primary{color:hsl(var(--primary));border-color:hsl(var(--primary)/.3)}button:disabled{opacity:.5;cursor:default}button:focus-visible,input:focus-visible,a:focus-visible{outline:2px solid hsl(var(--primary));outline-offset:-2px}.plan-settings{margin:0 20px 12px;padding:12px;background:hsl(var(--muted)/.4);border:1px solid hsl(var(--border));border-radius:6px;font-size:12px}.settings-line{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.settings-line+.settings-line{margin-top:12px}.settings-line input[type=number]{width:75px;padding:6px 8px;border:1px solid hsl(var(--border));border-radius:5px;background:hsl(var(--card));color:inherit;font:inherit}.filter-label{display:flex;align-items:center;gap:6px;cursor:pointer}.filter-label input{accent-color:hsl(var(--primary))}.setting-hint{font-size:11px;color:hsl(var(--muted-foreground))}.settings-line a{color:hsl(var(--primary));margin-left:12px;text-decoration:none}.plan-settings .danger{color:hsl(var(--destructive))}.remove-plan{margin-left:auto}.remove-confirm{display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin-top:12px;padding-top:12px;border-top:1px solid hsl(var(--border))}.remove-confirm span{flex:1;min-width:200px}.plan-error{margin:0 20px 12px;padding-left:9px;border-left:2px solid hsl(var(--destructive));font-size:12px;color:hsl(var(--destructive));overflow-wrap:anywhere}.plan-empty{padding:30px 20px;border:1px dashed hsl(var(--border));border-radius:8px;text-align:center;color:hsl(var(--muted-foreground))}.one-time-heading{margin:24px 0 10px;font-size:14px;font-weight:600}.one-time-heading span{margin-left:8px;font-size:11px;font-weight:400;color:hsl(var(--muted-foreground))}@media(max-width:900px){.plan-schedule{display:none}.plan-toggle{gap:10px;padding:15px 12px}.plan-metric{width:60px}}@media(max-width:600px){.plan-toolbar{align-items:stretch;flex-direction:column;gap:9px}.plan-toolbar input{width:100%}.plan-toggle{display:grid;grid-template-columns:14px 26px minmax(0,1fr) auto;gap:8px}.plan-toggle img{width:26px;height:26px}.plan-name strong{font-size:13px}.plan-state{grid-column:3;grid-row:2}.plan-metric{grid-column:4;grid-row:1;width:auto}.plan-metric strong{font-size:15px}.plan-record-count{grid-column:4;grid-row:2;width:auto}.plan-record-count span{display:inline;margin-left:7px}.plan-name small{font-size:10px}.plan-controls,.plan-context{padding-left:12px;padding-right:12px}.plan-controls{align-items:flex-start}.plan-actions{justify-content:flex-end}.plan-actions button{font-size:11px;padding:5px 7px}.plan-settings{margin-left:12px;margin-right:12px}.plan-error{margin-left:12px;margin-right:12px}.remove-plan{margin-left:0}}
.plan-progress{width:155px;flex-shrink:0;font-size:12px;line-height:1.5}.plan-progress strong{display:block;font-weight:600;color:hsl(var(--primary))}.plan-progress small{display:block;margin-top:3px;font-size:11px;color:hsl(var(--muted-foreground))}@media(max-width:1050px){.plan-metric{display:none}}@media(max-width:600px){.plan-progress{grid-column:3 / 5;grid-row:3;width:auto;display:flex;flex-wrap:wrap;gap:4px 12px}.plan-progress small{margin:0;align-self:center}.plan-metric{display:none}}
</style>
