const PREFIX = 'seekbox_';

export function loadLocal(key: string): string | null {
  if (typeof localStorage === 'undefined') return null;
  return localStorage.getItem(PREFIX + key);
}

export function saveLocal(key: string, value: string): void {
  localStorage.setItem(PREFIX + key, value);
}

// 旧版完整全选应包含新增来源；手动挑选的来源保持原有选择。
const LEGACY_PLUGINS = ["ikantv", "dyyjpro", "duoduo", "feikuai", "gaoqing888", "gying", "hunhepan", "jutoushe", "kkv", "lingjisp", "lou1", "melost", "ouge", "panlian", "qqpd", "quark4k", "quarksoo", "quarktv", "sousou", "thepiratebay", "wanou", "weibo", "xb6v", "xiaozhang", "yunso", "zxzj"];

export function loadPluginSelection(available?: string[]): string[] {
  const saved = loadLocal("plugins");
  if (saved === null) return [...(available || [])];
  const selected: unknown = saved ? JSON.parse(saved) : [];
  if (!Array.isArray(selected)) return [];
  const names = selected.filter((name): name is string => typeof name === "string");
  if (!available?.length) return names;
  const legacyAll = names.length === LEGACY_PLUGINS.length && LEGACY_PLUGINS.every(name => names.includes(name));
  return legacyAll ? [...available] : names.filter(name => available.includes(name));
}
