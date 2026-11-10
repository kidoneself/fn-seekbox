import { ref } from 'vue';
import { fetchCenterSites, type CenterSite } from '@/api/cloud';
export const centerSites = ref<CenterSite[]>([]);
export const centerSitesError = ref('');
let pending: Promise<void> | undefined;
let fetchedAt = 0;
export function refreshCenterSites() {
  if (pending) return pending;
  if (Date.now() - fetchedAt < 60000) return Promise.resolve();
  pending = fetchCenterSites().then(result => { centerSites.value = result.sites || []; fetchedAt = Date.now(); centerSitesError.value = result.stale ? '在线来源配置暂不可用，继续使用上次同步的配置。' : ''; })
    .catch(() => { centerSitesError.value = '在线来源配置暂不可用，继续使用本地来源。'; })
    .finally(() => { pending = undefined; });
  return pending;
}
// Remote configuration controls only built-in plugin selection, never executable code or credentials.
export function enabledCenterPlugins(ids: string[]) {
  const catalog = new Map(centerSites.value.map(site => [site.id, site]));
  return ids.filter(id => catalog.get(id)?.enabled !== false).sort((a,b) => (catalog.get(a)?.order ?? 999) - (catalog.get(b)?.order ?? 999));
}
export const centerPluginName = (id: string) => centerSites.value.find(site => site.id === id)?.name || id;
