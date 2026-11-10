// Run against local Vite. All checks and configuration writes use fixtures.
const { chromium } = require('../../app/auth/node_modules/playwright-core');
const assert = require('node:assert/strict');
const path = require('node:path');
(async () => {
  const browser = await chromium.launch({ executablePath: path.resolve(__dirname, '../../app/auth/chromium/chrome-headless-shell'), headless: true, args: ['--no-sandbox'] });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
    const errors = []; page.on('pageerror', error => errors.push(error.message));
    const now = Date.now();
    const center = { config: { daily_enabled: false, reporting_enabled: false, enabled_since: 0 }, connected: false, pending_events: 0, last_report_at: 0, last_error: '', rejected_events: 0, report_error: '' };
    const accounts = ['baidu', 'quark', 'xunlei'].map((provider, i) => ({ id: `account-${provider}`, provider, name: ['百度主账号', '夸克主账号', '迅雷主账号'][i], status: 'ready', last_error: '', created_at: now - 86400000, updated_at: now }));
    const stages = ['fetch_source', 'validate_account', 'prepare_target', 'transfer', 'verify_transfer', 'delete', 'verify_cleanup'];
    const run = (provider, id, minutes, patch = {}) => ({ id, account_id: `account-${provider}`, provider, status: 'passed', trigger: 'manual', created_at: now - minutes * 60000, finished_at: now - minutes * 60000 + 1500, cleanup_pending: false, stages: stages.map(stage => ({ stage, status: 'passed', duration_ms: 30 })), ...patch });
    const pending = run('baidu', 'baidu-cleanup', 120, { status: 'incomplete', cleanup_pending: true, cleanup_status: 'failed', target_name: '.seekbox-check-test-only', error: '百度历史待清理', stages: stages.map(stage => ({ stage, status: stage === 'delete' ? 'failed' : stage === 'verify_cleanup' ? 'skipped' : 'passed', duration_ms: 30 })) });
    let checks = [run('quark', 'quark-old', 90), pending, run('baidu', 'baidu-latest', 1), run('baidu', 'baidu-old', 180, { status: 'failed', error: '百度旧检查独有日志' }), run('quark', 'quark-latest', 2, { status: 'failed', error: '夸克当前检查独有日志' })];
    const queued = [], cleaned = [], configurations = [];
    await page.route('**/api/**', async route => {
      const request = route.request(), pathname = new URL(request.url()).pathname;
      if (!pathname.startsWith('/api/')) return route.continue();
      let data = {};
      if (pathname.endsWith('/health')) data = { status: 'ok', plugins: ['ikantv'] };
      else if (pathname.endsWith('/cloud/center/sites')) data = { sites: [] };
      else if (pathname.endsWith('/cloud/center')) {
        if (request.method() === 'PUT') { configurations.push(request.postDataJSON()); Object.assign(center.config, request.postDataJSON()); }
        data = center;
      } else if (pathname.endsWith('/cloud/accounts')) data = { accounts };
      else if (pathname.endsWith('/cloud/providers')) data = { providers: [] };
      else if (pathname.endsWith('/cloud/subscriptions')) data = { subscriptions: [] };
      else if (pathname.endsWith('/cloud/jobs')) data = { jobs: [] };
      else if (pathname.endsWith('/cloud/function-checks')) data = { checks };
      else if (pathname.endsWith('/function-check') && request.method() === 'POST') {
        const id = pathname.split('/').at(-2), account = accounts.find(item => item.id === id);
        assert(account, 'manual action identifies a fixture account'); queued.push(id);
        const check = run(account.provider, `${id}-manual`, 0, { status: 'queued', finished_at: 0, stages: stages.map(stage => ({ stage, status: 'skipped' })) });
        checks = [check, ...checks]; data = { check };
      } else if (pathname.endsWith('/cleanup') && request.method() === 'POST') {
        const id = pathname.split('/').at(-2), check = checks.find(item => item.id === id);
        assert(check?.cleanup_pending, 'cleanup identifies a pending run'); cleaned.push(id); check.cleanup_status = 'queued';
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) });
    });
    await page.goto(process.env.SEEKBOX_PREVIEW_URL || 'http://127.0.0.1:5178/');
    await page.locator('.workspace-navigation').getByRole('button', { name: '我的网盘', exact: true }).click();
    const card = provider => page.locator(`[data-account-id="account-${provider}"]`);
    const baidu = card('baidu'), quark = card('quark'), xunlei = card('xunlei');
    await baidu.waitFor();
    assert.equal(await page.locator('.cloud-account').count(), 3);
    assert.equal(await baidu.locator('.check-record').count(), 2, 'latest and old pending cleanup remain visible');
    assert.equal(await quark.locator('.check-record').count(), 1, 'only latest completed run starts visible');
    assert.equal(await xunlei.locator('.check-record').count(), 0); assert.match(await xunlei.innerText(), /尚未检查/);
    assert.equal(await page.locator('.check-record[open]').count(), 0, 'details start collapsed');
    assert.match(await baidu.locator('.check-record').first().innerText(), /检查通过/, 'unordered input is sorted by time');
    assert.match(await baidu.locator('.check-record').nth(1).innerText(), /待清理/);
    const blockedByCleanup = baidu.getByRole('button', { name: '请先清理', exact: true });
    assert(await blockedByCleanup.isDisabled(), 'unresolved cleanup prevents a new account check');
    await blockedByCleanup.evaluate(button => button.click()); assert.equal(queued.length, 0);
    assert.equal(await page.getByText('检查登录', { exact: true }).count(), 0);
    const settings = page.locator('.check-settings');
    const daily = settings.getByLabel('每天 00:00:01 自动检查（北京时间）', { exact: true });
    const reporting = settings.getByLabel('匿名运行统计', { exact: true });
    assert.equal(await settings.getAttribute('open'), null, 'settings start folded'); assert.equal(await daily.isVisible(), false);
    await settings.locator('summary').click(); assert.match(await settings.innerText(), /推广收益/);
    const changeSetting = async (input, key, value) => {
      const response = page.waitForResponse(result => result.url().endsWith('/cloud/center') && result.request().method() === 'PUT');
      await input.setChecked(value); await response;
      await page.waitForFunction(() => [...document.querySelectorAll('.check-settings input')].every(input => !input.disabled));
      assert.equal(center.config[key], value); assert.equal(await input.isChecked(), value);
    };
    await changeSetting(daily, 'daily_enabled', true); await changeSetting(daily, 'daily_enabled', false);
    await changeSetting(reporting, 'reporting_enabled', true); await changeSetting(reporting, 'reporting_enabled', false);
    assert.deepEqual(configurations, [{ daily_enabled: true }, { daily_enabled: false }, { reporting_enabled: true }, { reporting_enabled: false }]);
    await settings.locator('summary').click();
    await baidu.getByRole('button', { name: '历史检查（3）', exact: true }).click();
    assert.equal(await baidu.locator('.check-record').count(), 3); assert.equal(await quark.locator('.check-record').count(), 1, 'Baidu expansion does not expand Quark');
    await baidu.locator('.check-record').last().locator('summary').click();
    assert.match(await baidu.innerText(), /百度旧检查独有日志/); assert.doesNotMatch(await baidu.innerText(), /夸克当前检查独有日志/);
    await quark.locator('.check-record summary').click();
    assert.match(await quark.innerText(), /夸克当前检查独有日志/); assert.doesNotMatch(await quark.innerText(), /百度旧检查独有日志|百度历史待清理/);
    await quark.getByRole('button', { name: '历史检查（2）', exact: true }).click();
    assert.equal(await quark.locator('.check-record').count(), 2); assert.equal(await baidu.locator('.check-record').count(), 3);
    await quark.getByRole('button', { name: '收起历史', exact: true }).click(); await quark.locator('.check-record summary').click();
    await baidu.getByRole('button', { name: '收起历史', exact: true }).click();
    const latest = baidu.locator('.check-record').first(); await latest.locator('summary').click();
    assert.equal(await latest.locator('li').count(), 7);
    for (const label of ['获取测试源', '验证账号', '准备目录', '转存', '确认保存', '删除', '确认清理']) assert(await latest.getByText(label, { exact: true }).isVisible(), `missing stage ${label}`);
    const unresolved = baidu.locator('.check-record').filter({ has: page.locator('summary').filter({ hasText: '待清理' }) });
    await unresolved.locator('summary').click(); await unresolved.getByRole('button', { name: '重试清理', exact: true }).click();
    const clearing = baidu.getByRole('button', { name: '正在清理…', exact: true }); await clearing.waitFor(); assert(await clearing.isDisabled());
    await clearing.evaluate(button => button.click()); assert.deepEqual(cleaned, ['baidu-cleanup'], 'cleanup selects one run and prevents duplicate submission');
    await xunlei.getByRole('button', { name: '检查功能', exact: true }).click();
    const running = xunlei.getByRole('button', { name: '检查中…', exact: true }); await running.waitFor();
    assert(await running.isDisabled(), 'queued check blocks duplicates'); await running.evaluate(button => button.click()); assert.deepEqual(queued, ['account-xunlei']);
    const refresh = async () => {
      const response = page.waitForResponse(result => result.url().endsWith('/cloud/function-checks') && result.request().method() === 'GET');
      await page.getByRole('button', { name: '刷新', exact: true }).click(); await response;
      await page.waitForFunction(() => [...document.querySelectorAll('.cloud-header button')].every(button => !button.disabled));
    };
    checks[0].status = 'running'; await refresh();
    assert.match(await xunlei.locator('.check-record summary').innerText(), /检查中/);
    assert(await running.isDisabled(), 'running check blocks duplicates'); await running.evaluate(button => button.click()); assert.deepEqual(queued, ['account-xunlei']);
    checks[0].status = 'passed'; checks[0].finished_at = Date.now(); checks[0].stages = stages.map(stage => ({ stage, status: 'passed', duration_ms: 30 }));
    pending.cleanup_pending = false; pending.cleanup_status = 'passed'; await refresh();
    assert(await xunlei.getByRole('button', { name: '检查功能', exact: true }).isEnabled());
    assert(await baidu.getByRole('button', { name: '检查功能', exact: true }).isEnabled(), 'successful cleanup re-enables checking');
    assert.equal(await baidu.locator('.check-record').count(), 1, 'resolved old cleanup leaves the default view');
    assert.equal(await latest.getAttribute('open'), '', 'refresh preserves expanded latest record');
    await baidu.getByRole('button', { name: '历史检查（3）', exact: true }).click(); assert.match(await baidu.innerText(), /已补充清理/);
    await baidu.getByRole('button', { name: '收起历史', exact: true }).click();
    const noOverflow = async label => {
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${label}: document overflow`);
      for (const selector of ['.cloud-page', '.cloud-body']) assert(await page.locator(selector).evaluate(element => element.scrollWidth <= element.clientWidth + 1), `${label}: ${selector} overflow`);
    };
    await page.locator('.cloud-page').evaluate(element => { element.scrollTop = 0; }); await noOverflow('desktop expanded');
    await page.screenshot({ path: '/tmp/seekbox-function-check-1.5.17-desktop-details.png', fullPage: true });
    await latest.locator('summary').click(); await page.screenshot({ path: '/tmp/seekbox-function-check-1.5.17-desktop.png', fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 }); await latest.locator('summary').click();
    await page.locator('.cloud-page').evaluate(element => { element.scrollTop = 0; }); await noOverflow('mobile expanded');
    await page.screenshot({ path: '/tmp/seekbox-function-check-1.5.17-mobile.png', fullPage: true });
    await settings.locator('summary').click(); assert(await daily.isVisible()); await noOverflow('mobile settings'); assert.deepEqual(errors, []);
    console.log(JSON.stringify({ passed: true, desktop: '1280x800', mobile: '390x844', checks: ['per-account history isolation', 'latest plus unresolved cleanup', 'seven expanded stages', 'cleanup targets one run', 'cleanup duplicate submission blocked', 'folded settings retain both switches', 'queued and running duplicate checks blocked', 'completion re-enables manual check', 'refresh preserves expanded record', 'desktop and mobile overflow', 'no runtime errors'] }));
  } finally { await Promise.race([browser.close(), new Promise(resolve => setTimeout(resolve, 5000))]); }
})().then(() => process.exit(0)).catch(error => { console.error(error); process.exit(1); });
