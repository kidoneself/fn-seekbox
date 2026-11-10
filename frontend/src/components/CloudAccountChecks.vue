<script setup lang="ts">
import { computed, ref } from 'vue';
import { cloudTime, type FunctionCheck } from '@/api/cloud';

const props = defineProps<{ accountId: string; checks: FunctionCheck[]; busy: boolean }>();
defineEmits<{ cleanup: [id: string] }>();
const showAll = ref(false);
const records = computed(() => props.checks.filter(check => check.account_id === props.accountId).sort((a, b) => b.created_at - a.created_at));
// Unresolved cleanup stays visible even when a newer check has succeeded.
const visible = computed(() => showAll.value ? records.value : records.value.filter((check, index) => index === 0 || check.cleanup_pending));
const names: Record<string, string> = { fetch_source: '获取测试源', validate_account: '验证账号', prepare_target: '准备目录', transfer: '转存', verify_transfer: '确认保存', delete: '删除', verify_cleanup: '确认清理' };
const status: Record<string, string> = { queued: '等待执行', running: '检查中', passed: '检查通过', failed: '检查失败', incomplete: '未完成', skipped: '未执行', unknown: '待确认' };
const stageStatus: Record<string, string> = { passed: '通过', failed: '失败', skipped: '未执行', unknown: '待确认' };
</script>

<template>
  <div class="account-checks">
    <p v-if="!records.length" class="no-checks">尚未检查</p>
    <details v-for="check in visible" :key="check.id" class="check-record">
      <summary>
        <span class="check-state" :class="{ passed: check.status === 'passed', warning: check.status === 'failed' || check.cleanup_pending }">{{ status[check.status] || check.status }}</span>
        <span v-if="check.cleanup_pending" class="warning">{{ ['queued', 'running'].includes(check.cleanup_status || '') ? '正在清理' : '待清理' }}</span>
        <span v-else-if="check.cleanup_status === 'passed'" class="muted">已补充清理</span>
        <span class="check-time">{{ cloudTime(check.created_at) }} · {{ check.trigger === 'manual' ? '手动' : check.trigger === 'catch_up' ? '离线补检' : '自动' }}</span>
        <span class="detail-label">详情</span>
      </summary>
      <div class="check-detail">
        <ol v-if="check.stages.length">
          <li v-for="stage in check.stages" :key="stage.stage" :class="stage.status"><span>{{ names[stage.stage] || stage.stage }}</span><small>{{ stageStatus[stage.status] || stage.status }}<template v-if="stage.duration_ms != null"> · {{ (stage.duration_ms / 1000).toFixed(1) }} 秒</template></small></li>
        </ol>
        <p v-else class="muted">{{ check.status === 'queued' ? '等待执行，完成后自动更新。' : '暂无步骤记录。' }}</p>
        <p v-if="check.error" class="warning">{{ check.error }}</p>
        <div v-if="check.cleanup_pending" class="cleanup"><span>待清理目录：<code>{{ check.target_name }}</code></span><button type="button" :disabled="busy || ['queued', 'running'].includes(check.cleanup_status || '')" @click="$emit('cleanup', check.id)">{{ ['queued', 'running'].includes(check.cleanup_status || '') ? '正在清理…' : '重试清理' }}</button></div>
      </div>
    </details>
    <button v-if="records.length > visible.length || showAll" class="history" type="button" :aria-expanded="showAll" @click="showAll = !showAll">{{ showAll ? '收起历史' : `历史检查（${records.length}）` }}</button>
  </div>
</template>

<style scoped>
.account-checks { margin-top: 12px; padding-top: 8px; border-top: 1px solid hsl(var(--border)); font-size: 12px; }
.no-checks { margin: 3px 0; color: hsl(var(--muted-foreground)); }
.check-record + .check-record { border-top: 1px solid hsl(var(--border) / .6); }
.check-record summary { display: flex; align-items: center; gap: 10px; padding: 5px 0; cursor: pointer; list-style: none; flex-wrap: wrap; }
.check-record summary::-webkit-details-marker { display: none; }
.check-record summary::before { content: '›'; color: hsl(var(--muted-foreground)); transition: transform .15s; }
.check-record[open] > summary::before { transform: rotate(90deg); }
.check-record summary:focus-visible, button:focus-visible { outline: 2px solid hsl(var(--primary)); outline-offset: 3px; }
.check-state { font-weight: 600; }
.check-time, .muted, .detail-label { color: hsl(var(--muted-foreground)); }
.detail-label { margin-left: auto; }
.check-detail { padding: 8px 0 6px 15px; }
.check-detail ol { list-style: none; padding: 0; margin: 4px 0 12px; display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); gap: 12px; }
.check-detail li { display: flex; flex-direction: column; gap: 5px; border-left: 2px solid hsl(var(--border)); padding-left: 8px; }
.check-detail li.passed { border-color: hsl(var(--primary)); }
.check-detail li.failed { border-color: hsl(var(--destructive)); }
.check-detail small { color: hsl(var(--muted-foreground)); font-size: 11px; }
.check-detail p { margin: 6px 0; line-height: 1.7; overflow-wrap: anywhere; }
.passed { color: hsl(var(--primary)); }
.warning { color: hsl(var(--destructive)); }
.cleanup { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; color: hsl(var(--muted-foreground)); }
.cleanup span { min-width: 0; overflow-wrap: anywhere; }
button { cursor: pointer; padding: 5px 8px; border: 1px solid hsl(var(--border)); border-radius: 5px; background: hsl(var(--card)); color: hsl(var(--foreground)); font-size: 12px; }
button:disabled { opacity: .5; cursor: default; }
.history { border: 0; padding: 5px 0 0 15px; color: hsl(var(--muted-foreground)); }
@media (max-width: 700px) { .check-detail ol { grid-template-columns: repeat(2, minmax(0, 1fr)); } .check-record summary { gap: 7px; } }
</style>
