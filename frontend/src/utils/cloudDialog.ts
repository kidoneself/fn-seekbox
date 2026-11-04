import { nextTick, onUnmounted, watch, type Ref } from 'vue';

let openDialogs = 0;
let previousOverflow = '';

// Keep keyboard navigation inside the active dialog and return focus on close.
export function useCloudDialog(element: Ref<HTMLElement | null>, visible: Ref<boolean>) {
  let active = false;
  let previousFocus: HTMLElement | null = null;
  let activeElement: HTMLElement | null = null;
  const handleKey = (event: KeyboardEvent) => {
    if (event.key !== 'Tab' || !activeElement) return;
    const items = Array.from(activeElement.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]')).filter(item => item.getClientRects().length);
    const first = items[0];
    const last = items[items.length - 1];
    if (!first) { event.preventDefault(); activeElement.focus(); return; }
    if (event.shiftKey && (document.activeElement === first || document.activeElement === activeElement)) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && (document.activeElement === last || document.activeElement === activeElement)) { event.preventDefault(); first.focus(); }
  };
  const deactivate = () => {
    if (!active) return;
    active = false;
    activeElement?.removeEventListener('keydown', handleKey);
    activeElement = null;
    openDialogs = Math.max(0, openDialogs - 1);
    if (!openDialogs) document.body.style.overflow = previousOverflow;
    if (previousFocus?.isConnected) previousFocus.focus();
  };
  watch(visible, async value => {
    if (!value) { deactivate(); return; }
    await nextTick();
    if (!visible.value || active || !element.value) return;
    active = true;
    previousFocus = document.activeElement as HTMLElement | null;
    if (!openDialogs) previousOverflow = document.body.style.overflow;
    openDialogs++;
    document.body.style.overflow = 'hidden';
    activeElement = element.value;
    activeElement.addEventListener('keydown', handleKey);
    activeElement.focus();
  }, { immediate: true });
  onUnmounted(deactivate);
}
