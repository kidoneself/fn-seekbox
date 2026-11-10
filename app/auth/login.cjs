// One isolated, short-lived login per process. stdout is a private pipe to Go.
// No account/password prompts, shared browser profile, logs, or external broker.
'use strict';
const { request, chromium } = require(process.env.SEEKBOX_PLAYWRIGHT_MODULE || './node_modules/playwright-core');
const { randomUUID } = require('node:crypto');
const path = require('node:path');
const { cookieHeader: mergeCookies } = require('./cookies.cjs');
const provider = process.argv[2];
const ua = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36';
const deadline = Date.now() + 240000;
let http, browser;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const emit = value => process.stdout.write(JSON.stringify(value) + '\n');
const status = (state, message, extra = {}) => emit({ status: state, message, ...extra });
const address = (base, values) => base + '?' + new URLSearchParams(values);
const hosts = new Set(['passport.baidu.com', 'pan.baidu.com', 'uop.quark.cn', 'pan.quark.cn', 'drive-pc.quark.cn']);
const safeURL = value => {
  const u = new URL(value);
  if (u.protocol !== 'https:' || u.port || u.username || u.password || !hosts.has(u.hostname)) throw Error('unsupported destination');
  return u.href;
};
async function get(url, options = {}) {
  const response = await http.get(safeURL(url), { timeout: 18000, maxRedirects: 0, ...options });
  if (response.status() !== 200) throw Error('upstream response');
  return response;
}
async function json(url, options = {}) {
  const response = await get(url, options);
  const text = (await response.text()).trim();
  if (text.length > 1024 * 1024) throw Error('response too large');
  try { return JSON.parse(text); } catch {
    const match = text.match(/^[\w.]*\s*\(([\s\S]*)\)\s*;?$/);
    if (!match) throw Error('invalid response');
    return JSON.parse(match[1]);
  }
}
async function cookieHeader(host, extra = []) {
  const cookies = (await http.storageState()).cookies;
  return mergeCookies(cookies, host, extra);
}
async function baidu() {
  const qr = await json(address('https://passport.baidu.com/v2/api/getqrcode', { lp: 'pc', tpl: 'netdisk', apiver: 'v3' }));
  if (Number(qr.errno) !== 0 || !qr.sign) throw Error('no QR ticket');
  const image = await get(address('https://passport.baidu.com/v2/api/qrcode', { sign: qr.sign, lp: 'pc' }));
  const data = await image.body();
  if (!image.headers()['content-type']?.startsWith('image/') || data.length > 512000) throw Error('invalid QR image');
  status('waiting', '请用百度网盘 App 扫码，并在手机上确认登录', { image: 'data:image/png;base64,' + data.toString('base64') });
  let failures = 0;
  while (Date.now() < deadline) {
    let result;
    try { result = await json(address('https://passport.baidu.com/channel/unicast', { channel_id: qr.sign, callback: '' })); failures = 0; }
    catch { if (++failures >= 5) throw Error('poll failed'); await pause(1500); continue; }
    if (Number(result.errno) === 0 && result.channel_v) {
      const ticket = typeof result.channel_v === 'string' ? JSON.parse(result.channel_v) : result.channel_v;
      if (Number(ticket.status) === 0 && ticket.v) {
        status('verifying', '已确认登录，正在验证百度网盘账号');
        const response = await http.get(address('https://passport.baidu.com/v3/login/main/qrbdusslogin', {
          bduss: ticket.v, tpl: 'netdisk', u: 'https://pan.baidu.com/disk/main', loginVersion: 'v4', qrcode: '1', apiver: 'v3', tt: String(Date.now()),
        }), { timeout: 20000, maxRedirects: 0 });
        const pairs = response.headersArray().filter(h => h.name.toLowerCase() === 'set-cookie').map(h => h.value.split(';')[0]).filter(v => /^(BDUSS|STOKEN|BDUSS_BFESS|STOKEN_BFESS)=/.test(v));
        let next = 'https://pan.baidu.com/disk/main';
        for (let hop = 0; hop < 8; hop++) {
          const r = await http.get(safeURL(next), { timeout: 15000, maxRedirects: 0 });
          if ([301,302,303,307,308].includes(r.status()) && r.headers().location) { next = new URL(r.headers().location, next).href; continue; }
          break;
        }
        const cookie = await cookieHeader('pan.baidu.com', pairs);
        if (!cookie.includes('BDUSS=')) throw Error('missing login cookie');
        return { cookie };
      }
      status('scanned', '已扫码，请在百度网盘 App 确认登录');
    }
    await pause(1600);
  }
  return null;
}
async function quark() {
  const cas = (method, values = {}) => json(address('https://uop.quark.cn/cas/ajax/' + method, { client_id: '532', v: '1.2', request_id: randomUUID(), ...values }));
  const created = await cas('getTokenForQrcodeLogin');
  const token = created.data?.members?.token;
  if (Number(created.status) !== 2000000 || !token) throw Error('no QR ticket');
  const qr_url = address('https://su.quark.cn/4_eMHBJ', { token, client_id: '532', ssb: 'weblogin', uc_param_str: '', uc_biz_str: 'S:custom|OPT:SAREA@0|OPT:IMMERSIVE@1|OPT:BACK_BTN_STYLE@0' });
  status('waiting', '请用夸克 App 扫码，并在手机上确认登录', { qr_url });
  while (Date.now() < deadline) {
    const result = await cas('getServiceTicketByQrcodeToken', { token });
    if (Number(result.status) === 2000000 && result.data?.members?.service_ticket) {
      status('verifying', '已确认登录，正在验证夸克网盘账号');
      await json(address('https://pan.quark.cn/account/info', { st: result.data.members.service_ticket, lw: 'scan' }));
      await json(address('https://drive-pc.quark.cn/1/clouddrive/file/sort', { pr: 'ucpro', fr: 'pc', pdir_fid: '0', _page: '1', _size: '1' }), { headers: { Referer: 'https://pan.quark.cn/', Origin: 'https://pan.quark.cn' } });
      return { cookie: await cookieHeader('drive-pc.quark.cn') };
    }
    if ([50004002,50004003,50004004].includes(Number(result.status))) return null;
    if (Number(result.status) !== 50004001) throw Error('unexpected QR state');
    await pause(1800);
  }
  return null;
}
async function xunlei() {
  status('starting', '正在打开迅雷官方扫码登录页');
  browser = await chromium.launch({ headless: true, executablePath: process.env.SEEKBOX_CHROMIUM || path.join(__dirname, 'chromium', 'chrome-headless-shell'), args: ['--disable-dev-shm-usage'] });
  const context = await browser.newContext({ userAgent: ua, viewport: { width: 1100, height: 850 }, acceptDownloads: false });
  const page = await context.newPage();
  let token, meta = {}, headers = {};
  page.on('request', req => {
    try {
      const u = new URL(req.url());
      if (!['xluser-ssl.xunlei.com', 'api-pan.xunlei.com'].includes(u.hostname)) return;
      const values = req.headers();
      for (const key of ['x-device-id','x-client-id','x-captcha-token','x-client-version','x-device-sign','x-protocol-version','x-sdk-version']) if (values[key]) headers[key] = values[key];
      if (u.hostname === 'xluser-ssl.xunlei.com' && u.pathname === '/v1/shield/captcha/init') {
        const source = req.postDataJSON()?.meta;
        if (source) meta = Object.fromEntries(['client_version','package_name','user_id','timestamp','captcha_sign'].filter(k => ['string','number'].includes(typeof source[k])).map(k => [k,String(source[k])]));
      }
    } catch {}
  });
  page.on('response', async response => {
    try {
      const u = new URL(response.url());
      if (u.hostname !== 'xluser-ssl.xunlei.com' || !u.pathname.startsWith('/v1/auth/')) return;
      const value = await response.json();
      if (value.access_token && value.refresh_token) token = value;
    } catch {}
  });
  await page.goto('https://pan.xunlei.com/login/', { waitUntil: 'domcontentloaded', timeout: 45000 });
  const frame = page.frameLocator('iframe').first();
  await frame.locator('.xluweb-login-change__code').first().click({ timeout: 30000 });
  const image = frame.locator('.xluweb-login-qr-code__image img').first();
  await image.waitFor({ state: 'visible', timeout: 20000 });
  await image.evaluate(img => img.decode());
  status('waiting', '请用迅雷 App 扫码，并在手机上确认登录', { image: 'data:image/png;base64,' + (await image.screenshot()).toString('base64') });
  while (Date.now() < deadline) {
    if (token && headers['x-device-id'] && headers['x-client-id'] && headers['x-captcha-token']) {
      const expires = Math.min(86400, Math.max(60, Number(token.expires_in) || 3600));
      return { web_session: JSON.stringify({ access_token: token.access_token, refresh_token: token.refresh_token, expires_at: new Date(Date.now() + (expires - 30) * 1000).toISOString(), headers, captcha_meta: meta }) };
    }
    if (await frame.locator('.xluweb-login-qr-code__expired').isVisible().catch(() => false)) return null;
    await pause(1500);
  }
  return null;
}
async function dispose() { await browser?.close().catch(() => {}); await http?.dispose().catch(() => {}); }
process.on('SIGTERM', () => { dispose().finally(() => process.exit(0)); });
(async () => {
  try {
    if (!['baidu','quark','xunlei'].includes(provider)) throw Error('unsupported provider');
    http = await request.newContext({ userAgent: ua, timeout: 18000 });
    const credentials = await ({ baidu, quark, xunlei }[provider])();
    if (credentials) emit({ status: 'ready', credentials });
    else status('expired', '二维码已过期或登录已取消，请重新获取');
  } catch {
    // Upstream errors may include URLs/tickets. Never print their raw text.
    status('failed', '暂时无法完成扫码登录，请刷新二维码重试；如手机提示验证，请先在官方 App 完成');
  } finally { await dispose(); }
})().catch(() => process.exit(1));
