// Isolated browser contract checks. All API responses below are test fixtures.
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('../../app/auth/node_modules/playwright-core');
const root = path.resolve(__dirname, '../..');
let state, calls;
function reset(admin = true) {
  calls = [];
  state = { enabled: true, auto_enabled: true, can_manage: admin, current_version: '1.6.0', phase: 'available', available: true, release: { version: '1.6.1', notes: '优化转存和追更记录\n修复已知问题', size: 24000000, arch: 'amd64' }, last_checked_at: new Date().toISOString() };
}
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const route = url.pathname.replace(/^\/app\/fn-seekbox/, '');
  const json = value => { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(value)); };
  if (route.startsWith('/api/')) {
    if (route === '/api/health') return json({ status: 'ok', version: '1.6.0', plugins: [], plugins_enabled: true });
    if (route.startsWith('/api/update/')) {
      if (req.method !== 'GET') {
        assert.equal(req.headers['x-seekbox-action'], '1');
        calls.push([req.method, route]);
        if (!state.can_manage) { res.statusCode = 403; return json({ error: '没有权限' }); }
        if (route.endsWith('/settings')) {
          let data = ''; req.on('data', x => data += x); req.on('end', () => { state.auto_enabled = JSON.parse(data).auto_update; json(state); }); return;
        }
        if (route.endsWith('/install')) { state.phase = 'waiting_idle'; setTimeout(() => { state.current_version = '1.6.1'; state.phase = 'committed'; state.available = false; }, 250); }
      }
      return json(state);
    }
    return json({ accounts: [], providers: [], sites: [], checks: [], subscriptions: [], jobs: [] });
  }
  const filename = path.join(root, 'frontend/dist', route === '/' || route === '' ? 'index.html' : route);
  if (!filename.startsWith(path.join(root, 'frontend/dist') + '/')) { res.statusCode = 400; return res.end(); }
  try { res.setHeader('Content-Type', filename.endsWith('.js') ? 'application/javascript' : filename.endsWith('.css') ? 'text/css' : filename.endsWith('.html') ? 'text/html' : filename.endsWith('.svg') ? 'image/svg+xml' : filename.endsWith('.ico') ? 'image/x-icon' : 'application/octet-stream'); res.end(fs.readFileSync(filename)); }
  catch { res.statusCode = 404; res.end(); }
});
(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const browser = await chromium.launch({ executablePath: path.join(root, 'app/auth/chromium/chrome-headless-shell'), args: ['--no-sandbox'], headless: true });
  try {
    for (const width of [1280, 390, 320]) {
      reset();
      const page = await browser.newPage({ viewport: { width, height: 800 } });
      const errors = []; page.on('pageerror', e => errors.push(e.message));
      await page.goto(`http://127.0.0.1:${server.address().port}/app/fn-seekbox/`);
      await page.getByRole('button', { name: '当前版本 v1.6.0，软件更新' }).click();
      await page.getByRole('heading', { name: '软件更新', exact: true }).waitFor();
      await page.getByRole('heading', { name: '新版本 v1.6.1' }).waitFor();
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
      await page.getByRole('checkbox', { name: '自动更新', exact: true }).uncheck();
      await page.waitForFunction(() => !document.querySelector('input[type=checkbox]').disabled);
      assert.equal(state.auto_enabled, false);
      await page.getByRole('button', { name: '检查更新', exact: true }).click();
      await page.waitForFunction(() => !Array.from(document.querySelectorAll('button')).find(b => b.textContent === '立即更新').disabled);
      await page.getByRole('button', { name: '立即更新', exact: true }).click();
      await page.getByRole('button', { name: '刷新页面', exact: true }).waitFor({ timeout: 8000 });
      assert(calls.some(([method, route]) => method === 'POST' && route.endsWith('/install')));
      assert.deepEqual(errors, []);
      await page.screenshot({ path: `/tmp/seekbox-update-${width}.png`, fullPage: true });
      await page.close();
    }
    reset(false);
    const page = await browser.newPage();
    await page.goto(`http://127.0.0.1:${server.address().port}/app/fn-seekbox/`);
    await page.getByRole('button', { name: '当前版本 v1.6.0，软件更新' }).click();
    await page.getByRole('heading', { name: '新版本 v1.6.1' }).waitFor();
    assert.equal(await page.getByRole('button', { name: '立即更新', exact: true }).isDisabled(), true);
    assert.equal(await page.getByRole('checkbox', { name: '自动更新', exact: true }).isDisabled(), true);
    assert.deepEqual(calls, []);
    await page.close();
    console.log(JSON.stringify({ passed: true, widths: [1280,390,320], readonly_permissions: true, fixture_api: true }));
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(e => { console.error(e); server.close(); process.exitCode = 1; });
