<script setup lang="ts">
import { cloudTime, type FollowProgress } from '@/api/cloud';
defineProps<{ progress?: FollowProgress }>();
</script>

<template>
  <div class="progress-detail">
    <template v-if="progress">
      <div class="progress-values">
        <div><span>分享最新</span><strong>{{ progress.source.label || '未识别集数 / 期数' }}</strong><small v-if="progress.source.evidence">{{ progress.source.evidence }}</small></div>
        <div><span>已确认保存</span><strong>{{ progress.saved.label || (progress.confirmed_files ? '未识别集数 / 期数' : '暂无确认记录') }}</strong><small v-if="progress.saved.evidence">{{ progress.saved.evidence }}</small></div>
      </div>
      <p>{{ cloudTime(progress.checked_at) }} 读取分享 · 当前分享中 {{ progress.confirmed_files }} 个文件有成功保存记录。按正片文件名识别最高集 / 期，可能有缺集；已保存以历史成功记录为准。</p>
    </template>
    <p v-else>下次检查时识别集数、期数或更新日期；无法识别的文件会照常转存。</p>
  </div>
</template>

<style scoped>
.progress-detail{padding:12px 20px;font-size:12px}.progress-values{display:grid;grid-template-columns:1fr 1fr;gap:12px 24px}.progress-values>div{min-width:0}.progress-values span{color:hsl(var(--muted-foreground));margin-right:10px}.progress-values strong{font-weight:600}.progress-values small{display:block;margin-top:6px;overflow-wrap:anywhere;color:hsl(var(--muted-foreground));font-size:11px}.progress-detail p{margin:10px 0 0;font-size:11px;color:hsl(var(--muted-foreground));line-height:1.7}@media(max-width:600px){.progress-detail{padding:12px}.progress-values{grid-template-columns:1fr}}
</style>
