<script setup lang="ts">
import { cloudTime, type CenterStatus } from '@/api/cloud';
defineProps<{ center: CenterStatus | null; busy: boolean }>();
defineEmits<{ configure: [patch: { daily_enabled?: boolean; reporting_enabled?: boolean }] }>();
</script>

<template>
  <details v-if="center?.config" class="check-settings">
    <summary>检查设置</summary>
    <div class="settings-content">
      <label><input type="checkbox" :checked="center.config.daily_enabled" :disabled="busy" @change="$emit('configure', { daily_enabled: ($event.target as HTMLInputElement).checked })">每天 00:00:01 自动检查（北京时间）</label>
      <p>从公共测试分享转存小文件，再删除独立测试目录，确认转存与清理均成功。测试分享可能为推广链接，为项目带来推广收益。关闭页面后仍会执行；NAS 离线时恢复后补检。</p>
      <label><input type="checkbox" :checked="center.config.reporting_enabled" :disabled="busy" @change="$emit('configure', { reporting_enabled: ($event.target as HTMLInputElement).checked })">匿名运行统计</label>
      <p>向项目管理中心上报匿名实例、账号状态和检查结果，不上传 Cookie、文件名或分享链接。转存和删除均确认成功才计 1 次；网盘移入回收站不等于永久删除。</p>
      <p v-if="center.config.reporting_enabled">{{ center.connected ? '已登记' : '等待登记' }} · 最近上报 {{ cloudTime(center.last_report_at) }} · 待报 {{ center.pending_events }} 条<span v-if="center.rejected_events"> · 拒收或丢弃 {{ center.rejected_events }} 条</span></p>
      <p v-if="center.last_error || center.report_error" class="warning">{{ center.last_error || center.report_error }}</p>
    </div>
  </details>
</template>

<style scoped>
.check-settings { margin-top: 6px; color: hsl(var(--muted-foreground)); font-size: 12px; }
.check-settings summary { width: fit-content; padding: 8px 0; cursor: pointer; }
.check-settings summary:focus-visible { outline: 2px solid hsl(var(--primary)); outline-offset: 3px; }
.settings-content { padding: 14px 16px; border: 1px solid hsl(var(--border)); border-radius: 8px; background: hsl(var(--card)); }
.settings-content label { display: flex; align-items: center; gap: 8px; width: fit-content; color: hsl(var(--foreground)); cursor: pointer; }
.settings-content input { accent-color: hsl(var(--primary)); }
.settings-content p { margin: 6px 0 16px; line-height: 1.7; }
.settings-content p:last-child { margin-bottom: 0; }
.warning { color: hsl(var(--destructive)); }
</style>
