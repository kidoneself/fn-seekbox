<script setup lang="ts">
import { ref } from 'vue';
import supportImage from '@/assets/author-support.jpg';

const dialog = ref<HTMLDialogElement | null>(null);
const open = () => dialog.value?.showModal();
const close = () => dialog.value?.close();
</script>

<template>
  <section class="support-card" aria-label="支持作者">
    <div class="support-copy">
      <span class="support-title">喜欢小白搜盘？</span>
      <p>如果它帮到了你，欢迎请作者喝杯咖啡。</p>
    </div>
    <button type="button" class="support-button" @click="open">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
        <path d="M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 21l8.8-8.6a5.5 5.5 0 0 0 0-7.8Z" stroke-linejoin="round" />
      </svg>
      支持作者
    </button>
  </section>

  <Teleport to="body">
    <dialog ref="dialog" class="support-dialog" aria-labelledby="support-title" aria-describedby="support-description" @click="event => event.target === dialog && close()">
      <div class="support-dialog-content">
        <header>
          <div>
            <h2 id="support-title">感谢你的支持</h2>
            <p id="support-description">自愿赞赏，心意随意。</p>
          </div>
          <button type="button" class="support-close" aria-label="关闭赞赏码" autofocus @click="close">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6" stroke-linecap="round" /></svg>
          </button>
        </header>
        <img :src="supportImage" alt="作者提供的赞赏码" width="1296" height="1296" />
        <a :href="supportImage" download="小白搜盘-赞赏码.jpg">保存赞赏码</a>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.support-card { display: flex; align-items: center; flex-direction: column; justify-content: center; gap: .9rem; min-width: 0; max-width: 100%; padding: 1rem 1.1rem; border: 1px solid hsl(var(--border)); border-radius: .75rem; background: hsl(var(--card)); text-align: center; }
.support-copy { min-width: 0; }
.support-title { font-size: .8125rem; font-weight: 600; color: hsl(var(--foreground)); }
.support-copy p { margin: .3rem 0 0; color: hsl(var(--muted-foreground)); font-size: .75rem; line-height: 1.6; }
.support-button { display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; gap: .4rem; padding: .5rem .7rem; border: 1px solid hsl(var(--primary) / .2); border-radius: .45rem; background: hsl(var(--primary) / .06); color: hsl(var(--primary)); font-size: .75rem; font-weight: 600; cursor: pointer; }
.support-button:hover { background: hsl(var(--primary) / .12); }
.support-button svg { width: 1rem; height: 1rem; }
.support-dialog { width: min(26rem, calc(100vw - 2rem)); max-height: calc(100dvh - 2rem); margin: auto; padding: 0; overflow: auto; border: 1px solid hsl(var(--border)); border-radius: 1rem; background: hsl(var(--card)); color: hsl(var(--foreground)); box-shadow: 0 20px 60px #0003; }
.support-dialog::backdrop { background: #10182880; }
.support-dialog-content { padding: 1.1rem; }
header { display: flex; align-items: flex-start; justify-content: space-between; gap: .75rem; text-align: left; }
h2 { margin: 0; font-size: 1rem; font-weight: 600; }
header p { margin: .35rem 0 0; color: hsl(var(--muted-foreground)); font-size: .75rem; }
.support-close { display: inline-flex; align-items: center; justify-content: center; width: 2rem; height: 2rem; flex-shrink: 0; border: 0; border-radius: .4rem; background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); cursor: pointer; }
.support-close svg { width: 1rem; height: 1rem; }
img { display: block; width: 100%; height: auto; margin-top: 1rem; border-radius: .5rem; }
a { display: block; margin-top: .9rem; text-align: center; font-size: .8125rem; color: hsl(var(--primary)); text-decoration: underline; text-underline-offset: .2rem; }
button:focus-visible, a:focus-visible { outline: 2px solid hsl(var(--primary)); outline-offset: 3px; }
@media (max-width: 480px) { .support-card { flex-direction: column; gap: .8rem; width: 100%; text-align: center; } }
</style>
