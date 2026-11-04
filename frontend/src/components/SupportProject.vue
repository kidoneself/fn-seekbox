<script setup lang="ts">
import supportImage from '@/assets/author-support-code.png';
import officialAccountImage from '@/assets/official-account.jpg';
</script>
<template>
  <section class="project-support" aria-label="支持项目">
    <div class="support-grid">
      <div class="project-links">
        <h2>支持项目</h2>
        <p>查看源码、反馈问题，或点亮一颗 Star。</p>
        <div class="project-actions">
          <a class="star-link" href="https://github.com/kidoneself/fn-seekbox" target="_blank" rel="noopener noreferrer"><span aria-hidden="true">☆</span> GitHub · Star <span aria-hidden="true">↗</span></a>
          <a class="explore-link" href="https://juzhenpan.com" target="_blank" rel="noopener noreferrer">矩阵盘 <span aria-hidden="true">↗</span></a>
        </div>
      </div>
      <div class="code-card">
        <div><h3>关注公众号</h3><p>获取更新通知</p></div>
        <a :href="officialAccountImage" target="_blank" rel="noopener noreferrer" aria-label="查看公众号二维码"><img :src="officialAccountImage" alt="作者提供的公众号二维码" width="258" height="258"></a>
      </div>
      <div class="code-card">
        <div><h3>随心赞赏</h3><p>心意随意，感谢支持</p></div>
        <a :href="supportImage" target="_blank" rel="noopener noreferrer" aria-label="查看赞赏小程序码"><img :src="supportImage" alt="作者提供的赞赏小程序码" width="620" height="620"></a>
      </div>
    </div>
  </section>
</template>
<style scoped>
.project-support { margin-top: auto; padding: 20px 24px; border: 1px solid hsl(var(--border)); border-radius: 8px; background: hsl(var(--card)); text-align: left; }
.support-grid { display: grid; grid-template-columns: 1.15fr 1fr 1fr; align-items: center; }
h2,h3 { margin: 0; font-size: 13px; font-weight: 600; } p { margin: 7px 0 0; color: hsl(var(--muted-foreground)); font-size: 11px; line-height: 1.6; }
a { text-decoration: none; }.project-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }.project-actions a { display: inline-flex; align-items: center; gap: 7px; min-height: 32px; padding: 6px 10px; border: 1px solid hsl(var(--border)); border-radius: 5px; color: hsl(var(--foreground)); font-size: 12px; }.project-actions .star-link { color: #956014; border-color: #eadbc3; background: #fffaf2; }.project-actions a:hover { border-color: hsl(var(--primary)); }
.code-card { display: flex; flex-direction: row-reverse; justify-content: flex-end; align-items: center; gap: 16px; padding-left: 24px; margin-left: 24px; border-left: 1px solid hsl(var(--border)); }.code-card > a { flex-shrink: 0; display: block; }
img { display: block; width: 112px; height: 112px; object-fit: contain; background: white; }
a:focus-visible { outline: 2px solid hsl(var(--primary)); outline-offset: 3px; }
@media(min-width:761px) and (max-width:1100px) { .project-support { padding: 16px; }.support-grid { grid-template-columns: 1.15fr 1fr 1fr; }.code-card { margin-left: 14px; padding-left: 14px; gap: 6px; }.code-card img { width: 96px; height: 96px; }.project-actions { gap: 6px; }.project-actions a { padding: 6px; font-size: 11px; } }
@media(max-width:760px) { .project-support { margin-top: 0; padding: 16px; }.support-grid { grid-template-columns: 1fr 1fr; gap: 18px 14px; }.project-links { grid-column: 1 / -1; padding-bottom: 16px; border-bottom: 1px solid hsl(var(--border)); }.code-card { margin: 0; padding: 0; border: 0; flex-direction: column; align-items: start; gap: 10px; }.code-card img { width: 112px; height: 112px; }.project-actions { margin-top: 10px; } }
</style>
