'use strict';

// The final cookie jar is authoritative. Early login response cookies are
// only a fallback when the redirect chain did not retain that name.
function cookieHeader(cookies, host, extra = [], now = Date.now() / 1000) {
  const values = new Map();
  for (const pair of extra) {
    const index = pair.indexOf('=');
    if (index > 0 && index < pair.length - 1) values.set(pair.slice(0, index), pair.slice(index + 1));
  }
  // Prefer a host-specific cookie over a parent-domain cookie of the same name.
  const matching = cookies.filter(c => {
    const domain = c.domain.replace(/^\./, '');
    return host === domain || host.endsWith('.' + domain);
  }).sort((a, b) => a.domain.replace(/^\./, '').length - b.domain.replace(/^\./, '').length);
  for (const c of matching) {
    if (!c.value || (c.expires > 0 && c.expires <= now)) {
      values.delete(c.name);
      continue;
    }
    values.set(c.name, c.value);
  }
  return [...values].map(([key, value]) => `${key}=${value}`).join('; ');
}

module.exports = { cookieHeader };
