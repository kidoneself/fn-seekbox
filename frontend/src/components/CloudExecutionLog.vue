<script setup lang="ts">
import { computed, ref } from 'vue';
import { cloudJobActive, cloudJobStatus, cloudTime, type CloudJob, type CloudAccount } from '@/api/cloud';
import { getDiskTypeName } from '@/utils/diskTypes';
import CloudProgressDetail from './CloudProgressDetail.vue';
const props = withDefaults(defineProps<{ jobs: CloudJob[]; accounts?: CloudAccount[]; standalone?: boolean }>(), { standalone: false, accounts: () => [] });
const expanded = ref(new Set<string>());
const limit = ref(6);
const sorted = computed(() => [...props.jobs].sort((a, b) => b.created_at - a.created_at || b.id.localeCompare(a.id)));
const visible = computed(() => sorted.value.slice(0, limit.value));
const toggle = (id: string) => { const next = new Set(expanded.value); next.has(id) ? next.delete(id) : next.add(id); expanded.value = next; };
const failed = (job: CloudJob) => ['failed', 'partial', 'interrupted'].includes(job.status);
const label = (job: CloudJob) => job.subscription_id && job.status === 'succeeded' ? '检查完成' : cloudJobStatus(job.status);
const fullTime = (value: number) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未结束';
const duration = (job: CloudJob) => {
  if (!job.finished_at) return cloudJobActive(job) ? '进行中' : '—';
  const seconds = Math.max(0, Math.round((job.finished_at - job.created_at) / 1000));
  return seconds < 60 ? `${seconds} 秒` : `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
};
</script>

<template>
  <div class="execution-log">
    <p v-if="!jobs.length" class="log-empty">还没有执行记录。计划检查完成后会显示在这里。</p>
    <template v-else>
      <div class="log-scroll" role="region" aria-label="执行记录表格" tabindex="0">
        <table>
          <thead><tr><th>{{ standalone ? '转存任务 / 提交时间' : '提交时间' }}</th><th>执行结果</th><th class="number">保存</th><th class="number">跳过</th><th class="number">过滤</th><th class="number">总计</th><th>用时</th><th><span class="sr-only">查看日志</span></th></tr></thead>
          <tbody>
            <template v-for="job in visible" :key="job.id">
              <tr :class="{ 'is-expanded': expanded.has(job.id) }">
                <td><strong v-if="standalone" class="job-title">{{ job.title || '分享资源' }}</strong><span v-if="standalone" class="job-account">{{ getDiskTypeName(job.provider) }} · {{ accounts.find(a => a.id === job.account_id)?.name || '账号已移除' }}</span><time :datetime="new Date(job.created_at).toISOString()">{{ cloudTime(job.created_at) }}</time></td>
                <td><span class="run-state" :class="{ bad: failed(job), active: cloudJobActive(job), done: job.status === 'succeeded' }">{{ label(job) }}</span></td>
                <td class="number saved">{{ job.saved }}</td><td class="number">{{ job.skipped }}</td><td class="number">{{ job.filtered || 0 }}</td><td class="number">{{ job.total }}</td>
                <td class="duration">{{ duration(job) }}</td>
                <td><button type="button" class="log-toggle" :aria-expanded="expanded.has(job.id)" :aria-controls="'run-' + job.id" @click="toggle(job.id)">{{ expanded.has(job.id) ? '收起' : '日志' }}<span v-if="job.error && !expanded.has(job.id)" class="error-dot" aria-label="有错误信息"></span></button></td>
              </tr>
              <tr v-if="expanded.has(job.id)" :id="'run-' + job.id" class="log-detail"><td colspan="8">
                <dl><div><dt>提交</dt><dd>{{ fullTime(job.created_at) }}</dd></div><div><dt>结束</dt><dd>{{ fullTime(job.finished_at) }}</dd></div><div><dt>任务编号</dt><dd class="job-id">{{ job.id }}</dd></div></dl>
                <p v-if="job.error" class="execution-error" role="status">{{ job.error }}</p>
                <p v-else-if="job.status === 'succeeded'">{{ job.saved ? `本次保存 ${job.saved} 个文件。` : '本次没有新增保存文件。' }}跳过 {{ job.skipped }} 个，过滤广告附件 {{ job.filtered || 0 }} 个。</p>
                <p v-else>{{ label(job) }}，{{ cloudJobActive(job) ? '结果会自动更新。' : '请检查网盘中的实际结果。' }}</p>
                <CloudProgressDetail v-if="job.progress" :progress="job.progress" />
                <small>用时包含排队等待；这里展示任务结果，暂无逐文件日志。</small>
              </td></tr>
            </template>
          </tbody>
        </table>
      </div>
      <footer class="log-footer"><span>已保留 {{ jobs.length }} 次执行记录</span><button v-if="sorted.length > limit" type="button" @click="limit += 6">查看更多（剩余 {{ sorted.length - limit }} 条）</button></footer>
    </template>
  </div>
</template>

<style scoped>
.execution-log{min-width:0;font-size:12px}.log-scroll{position:relative;overflow-x:auto;max-width:100%}.log-scroll:focus-visible{outline:2px solid hsl(var(--primary));outline-offset:-2px}table{width:100%;border-collapse:collapse;text-align:left;font-variant-numeric:tabular-nums}th{padding:10px 14px;color:hsl(var(--muted-foreground));font-size:11px;font-weight:500;background:hsl(var(--muted)/.45);white-space:nowrap}td{padding:11px 14px;border-bottom:1px solid hsl(var(--border)/.65);white-space:nowrap}th:first-child,td:first-child{padding-left:20px}.number{text-align:right}.saved{font-weight:650}.run-state{display:inline-flex;align-items:center;gap:6px}.run-state:before{content:'';width:5px;height:5px;border-radius:50%;background:currentColor}.done{color:hsl(var(--primary))}.bad,.error-dot{color:hsl(var(--destructive))}.active{color:#a56808}.duration,time{color:hsl(var(--muted-foreground))}.log-toggle,.log-footer button{border:0;background:none;color:hsl(var(--primary));font:inherit;cursor:pointer;padding:4px}.log-toggle{display:inline-flex;align-items:center;gap:5px}.error-dot{width:5px;height:5px;background:currentColor;border-radius:50%}.log-toggle:focus-visible,.log-footer button:focus-visible{outline:2px solid hsl(var(--primary));outline-offset:2px}.log-detail td{white-space:normal;background:hsl(var(--muted)/.3);padding:14px 20px}.log-detail dl{display:flex;flex-wrap:wrap;gap:12px 28px;margin:0}.log-detail dl div{display:flex;gap:8px}.log-detail dt,.log-detail small{color:hsl(var(--muted-foreground))}.log-detail dd{margin:0;overflow-wrap:anywhere}.log-detail p{margin:12px 0 5px;white-space:pre-wrap;overflow-wrap:anywhere}.job-id{font-family:monospace;font-size:11px}.execution-error{color:hsl(var(--destructive))}.log-footer{display:flex;align-items:center;justify-content:space-between;padding:10px 20px;color:hsl(var(--muted-foreground));font-size:11px;gap:12px}.job-account{display:block;margin-bottom:4px;color:hsl(var(--muted-foreground));font-size:11px}.job-title{display:block;max-width:360px;overflow:hidden;text-overflow:ellipsis;margin-bottom:4px;font-size:12px;font-weight:550}.log-empty{padding:22px 20px;margin:0;color:hsl(var(--muted-foreground))}.sr-only{position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}@media(max-width:600px){td,th{padding:10px}.log-detail dl{display:grid;gap:8px}.job-title{max-width:190px}.log-scroll table{min-width:650px}}
</style>
