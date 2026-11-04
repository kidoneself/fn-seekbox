<script setup lang="ts">
import SupportAuthor from '@/components/SupportAuthor.vue';
import { appPath } from '@/utils/appPath';

defineProps<{ siteUrl: string }>();
</script>

<template>
  <div class="home-promotions">
    <section class="site-card" aria-label="矩阵盘主站">
      <div class="site-heading">
        <img :src="appPath('/logo.svg')" alt="" width="40" height="40" />
        <div>
          <span class="site-label">主站</span>
          <h2>矩阵盘</h2>
        </div>
      </div>
      <p>网盘资源搜索与发现，更多内容尽在矩阵盘。</p>
      <a :href="siteUrl" target="_blank" rel="noopener noreferrer" class="site-link">
        访问主站
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <path d="M14 5h5v5M19 5l-9 9M10 5H6a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-4" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </a>
    </section>
    <SupportAuthor />
  </div>
</template>

<style scoped>
.home-promotions { display: grid; grid-template-columns: 1.15fr 1fr; gap: .8rem; width: min(42rem, 100%); margin-top: 1.8rem; text-align: left; }
.site-card { padding: 1.1rem 1.2rem; border: 1px solid hsl(var(--primary) / .18); border-radius: .75rem; background: linear-gradient(125deg, hsl(var(--primary) / .05), hsl(var(--card))); }
.site-heading { display: flex; align-items: center; gap: .65rem; }
.site-heading img { display: block; flex-shrink: 0; }
.site-label { font-size: .65rem; color: hsl(var(--muted-foreground)); }
h2 { margin: .05rem 0 0; color: hsl(var(--foreground)); font-size: .95rem; font-weight: 650; }
p { margin: .85rem 0; color: hsl(var(--muted-foreground)); font-size: .75rem; line-height: 1.7; }
.site-link { display: inline-flex; align-items: center; gap: .4rem; font-size: .75rem; font-weight: 600; color: hsl(var(--primary)); text-decoration: none; }
.site-link:hover { text-decoration: underline; text-underline-offset: .2rem; }
.site-link:focus-visible { outline: 2px solid hsl(var(--primary)); outline-offset: 4px; border-radius: .15rem; }
.site-link svg { width: .9rem; height: .9rem; }
@media (max-width: 600px) { .home-promotions { grid-template-columns: 1fr; max-width: 24rem; } }
</style>
