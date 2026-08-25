#!/usr/bin/env python3
"""Serve the 小白搜盘 frontend and reverse-proxy API/plugin paths to the backend."""
from __future__ import annotations

import http.client
import json
import os
import posixpath
import ssl
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import parse_qs, urlencode, urlsplit

WEBROOT = os.path.abspath(os.environ.get("WEBROOT", "./web"))
BACKEND_HOST = os.environ.get("BACKEND_HOST", os.environ.get("PANSOU_HOST", "127.0.0.1"))
BACKEND_PORT = int(os.environ.get("BACKEND_PORT", os.environ.get("PANSOU_PORT", "18888")))
LISTEN_HOST = os.environ.get("GATEWAY_HOST", "0.0.0.0")
LISTEN_PORT = int(os.environ.get("APP_PORT", os.environ.get("GATEWAY_PORT", "12668")))

PROXY_PREFIXES = ("/api", "/qqpd", "/gying", "/weibo", "/panlian")
SKIP_REQUEST_HEADERS = {
    "host",
    "connection",
    "keep-alive",
    "proxy-connection",
    "transfer-encoding",
    "te",
    "trailer",
    "upgrade",
}
SKIP_RESPONSE_HEADERS = {
    "connection",
    "keep-alive",
    "proxy-connection",
    "transfer-encoding",
    "te",
    "trailer",
}

# Probe the plugin's real site from this NAS, not from the browser.
PLUGIN_PROBE = {
    "ikantv": "https://api.naspt.vip/",
    "jutoushe": "https://1.star2.cn/",
    "lou1": "http://www.1lou.me",
    "wanou": "https://woog.nxog.eu.org/",
    "duoduo": "https://tv.yydsys.top/",
    "dyyjpro": "https://dyyjpro.com",
    "lingjisp": "https://web5.mukaku.com/prod/api/v1/",
    "ouge": "https://woog.nxog.eu.org/",
    "quarktv": "https://www.quarktv.com",
    "feikuai": "https://feikuai.tv/",
    "gaoqing888": "https://www.gaoqing888.com",
    "gying": "https://www.gying.net",
    "hunhepan": "https://hunhepan.com/search",
    "kkv": "http://kkv.q-23.cn",
    "melost": "https://www.melost.cn",
    "panlian": "https://pinglian.lol",
    "qqpd": "https://pd.qq.com/",
    "quark4k": "https://quark4k.com/",
    "quarksoo": "https://quarksoo.cc/",
    "sousou": "https://sousou.pro/",
    "thepiratebay": "https://thpibay.xyz/",
    "weibo": "https://m.weibo.cn/",
    "xb6v": "https://www.66ss.org",
    "xiaozhang": "https://xzys.fun",
    "yunso": "https://www.yunso.net",
    "zxzj": "https://www.zxzjys.com",
}
PING_UA = "Mozilla/5.0 (compatible; SeekBoxPing/1.0)"
PING_TTL = 180
PING_SLOW_MS = 1500
SEARCH_KW = os.environ.get("PLUGIN_PROBE_KW", "电影")
SEARCH_TIMEOUT = 25
_ping_lock = threading.Lock()
_ping_cache: dict[str, dict] = {}
_ping_ready = False
_ping_started = 0.0
_ping_running = False



def plugin_names() -> list[str]:
    env = os.environ.get("ENABLED_PLUGINS", "")
    names = [p.strip() for p in env.split(",") if p.strip()]
    if names:
        return names
    return list(PLUGIN_PROBE.keys())


def _probe_url(url: str) -> dict:
    t0 = time.monotonic()
    parsed = urlsplit(url)
    host = parsed.hostname
    if not host:
        return {"state": "down", "ms": 0}
    path = parsed.path or "/"
    if parsed.query:
        path += "?" + parsed.query
    conn = None
    try:
        if parsed.scheme == "https":
            ctx = ssl._create_unverified_context()
            conn = http.client.HTTPSConnection(
                host, parsed.port or 443, timeout=5, context=ctx
            )
        else:
            conn = http.client.HTTPConnection(host, parsed.port or 80, timeout=5)
        conn.request(
            "GET",
            path,
            headers={
                "User-Agent": PING_UA,
                "Accept": "*/*",
                "Connection": "close",
            },
        )
        resp = conn.getresponse()
        resp.read(2048)
        ms = int((time.monotonic() - t0) * 1000)
        state = "ok" if ms < PING_SLOW_MS else "slow"
        return {"state": state, "ms": ms}
    except Exception:
        ms = int((time.monotonic() - t0) * 1000)
        return {"state": "down", "ms": ms}
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass


def _blank_row() -> dict:
    return {
        "net": {"state": "wait", "ms": 0},
        "use": {"state": "wait", "ms": 0, "hits": 0},
    }


def _count_hits(data: dict) -> int:
    body = data.get("data") if isinstance(data.get("data"), dict) else data
    if not isinstance(body, dict):
        return 0
    total = body.get("total")
    if isinstance(total, int) and total >= 0:
        return total
    merged = body.get("merged_by_type") or {}
    n = 0
    if isinstance(merged, dict):
        for items in merged.values():
            if isinstance(items, list):
                n += len(items)
    if n:
        return n
    results = body.get("results")
    if isinstance(results, list):
        return len(results)
    return 0


def _probe_search(name: str) -> dict:
    t0 = time.monotonic()
    conn = None
    try:
        body = json.dumps(
            {
                "kw": SEARCH_KW,
                "src": "plugin",
                "plugins": [name],
                "res": "merge",
                "refresh": True,
            },
            ensure_ascii=False,
        ).encode("utf-8")
        conn = http.client.HTTPConnection(
            BACKEND_HOST, BACKEND_PORT, timeout=SEARCH_TIMEOUT
        )
        conn.request(
            "POST",
            "/api/search",
            body=body,
            headers={
                "Content-Type": "application/json; charset=utf-8",
                "Content-Length": str(len(body)),
                "Host": "%s:%s" % (BACKEND_HOST, BACKEND_PORT),
            },
        )
        resp = conn.getresponse()
        payload = resp.read()
        ms = int((time.monotonic() - t0) * 1000)
        if resp.status != 200:
            return {"state": "fail", "ms": ms, "hits": 0}
        data = json.loads(payload.decode("utf-8"))
        if not isinstance(data, dict):
            return {"state": "fail", "ms": ms, "hits": 0}
        code = data.get("code")
        if code not in (0, None):
            return {"state": "fail", "ms": ms, "hits": 0}
        hits = _count_hits(data)
        if hits > 0:
            return {"state": "ok", "ms": ms, "hits": hits}
        return {"state": "empty", "ms": ms, "hits": 0}
    except Exception:
        ms = int((time.monotonic() - t0) * 1000)
        return {"state": "fail", "ms": ms, "hits": 0}
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass


def _merge_part(name: str, key: str, row: dict) -> None:
    with _ping_lock:
        cur = _ping_cache.get(name) or _blank_row()
        cur[key] = row
        _ping_cache[name] = cur


def _run_pings(names: list[str]) -> None:
    global _ping_ready, _ping_running
    try:
        with ThreadPoolExecutor(max_workers=8) as pool:
            futs = {}
            for name in names:
                url = PLUGIN_PROBE.get(name)
                if not url:
                    _merge_part(name, "net", {"state": "down", "ms": 0})
                    continue
                futs[pool.submit(_probe_url, url)] = name
            for fut in as_completed(futs):
                name = futs[fut]
                try:
                    row = fut.result()
                except Exception:
                    row = {"state": "down", "ms": 0}
                _merge_part(name, "net", row)

        with ThreadPoolExecutor(max_workers=3) as pool:
            futs = {pool.submit(_probe_search, name): name for name in names}
            for fut in as_completed(futs):
                name = futs[fut]
                try:
                    row = fut.result()
                except Exception:
                    row = {"state": "fail", "ms": 0, "hits": 0}
                _merge_part(name, "use", row)
    finally:
        with _ping_lock:
            _ping_ready = True
            _ping_running = False


def start_pings(force: bool = False) -> None:
    global _ping_started, _ping_ready, _ping_running
    now = time.time()
    with _ping_lock:
        fresh = bool(_ping_cache) and (now - _ping_started) < PING_TTL
        if _ping_running:
            return
        if fresh and not force:
            return
        _ping_started = now
        _ping_ready = False
        _ping_running = True
        _ping_cache.clear()
        names = plugin_names()
        for name in names:
            _ping_cache[name] = _blank_row()
    threading.Thread(target=_run_pings, args=(names,), daemon=True).start()


def ping_snapshot() -> dict:
    start_pings(False)
    with _ping_lock:
        return {
            "ready": _ping_ready,
            "kw": SEARCH_KW,
            "plugins": {
                k: {
                    "net": dict((v or {}).get("net") or {}),
                    "use": dict((v or {}).get("use") or {}),
                }
                for k, v in _ping_cache.items()
            },
        }


def is_proxy_path(path: str) -> bool:
    return any(path == p or path.startswith(p + "/") for p in PROXY_PREFIXES)


def safe_join(root: str, url_path: str) -> str | None:
    rel = posixpath.normpath(urlsplit(url_path).path)
    if rel.startswith(".."):
        return None
    if rel in ("", "/"):
        rel = "index.html"
    else:
        rel = rel.lstrip("/")
    full = os.path.abspath(os.path.join(root, rel))
    if not full.startswith(root + os.sep) and full != root:
        return None
    return full


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt: str, *args) -> None:
        sys.stderr.write("%s - %s\n" % (self.log_date_time_string(), fmt % args))

    def do_GET(self) -> None:
        self._dispatch()

    def do_POST(self) -> None:
        self._dispatch()

    def do_PUT(self) -> None:
        self._dispatch()

    def do_PATCH(self) -> None:
        self._dispatch()

    def do_DELETE(self) -> None:
        self._dispatch()

    def do_HEAD(self) -> None:
        self._dispatch()

    def do_OPTIONS(self) -> None:
        self._dispatch()

    def _dispatch(self) -> None:
        path = urlsplit(self.path).path
        if path == "/api/plugin-ping":
            self._plugin_ping()
            return
        if is_proxy_path(path):
            self._proxy()
            return
        self._static()

    def _plugin_ping(self) -> None:
        qs = parse_qs(urlsplit(self.path).query)
        if qs.get("refresh", [""])[0] in ("1", "true", "yes"):
            start_pings(True)
        payload = json.dumps(ping_snapshot(), ensure_ascii=False).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(payload)

    def _timeout_for_path(self) -> int:
        path = urlsplit(self.path).path
        if path.startswith("/gying/") or path == "/api/search":
            return 180
        if path == "/api/health":
            return 10
        if path == "/api/check/links":
            return 120
        return 60

    def _force_plugin_only(self, raw_path: str, body: bytes | None) -> tuple[str, bytes | None]:
        """Drop Telegram: search plugins only, never forward channel lists."""
        parts = urlsplit(raw_path)
        path = parts.path
        if path != "/api/search":
            return raw_path, body
        if self.command == "GET":
            qs = parse_qs(parts.query, keep_blank_values=True)
            qs["src"] = ["plugin"]
            qs.pop("channels", None)
            new_q = urlencode(qs, doseq=True)
            return path + (("?" + new_q) if new_q else ""), body
        if body:
            try:
                data = json.loads(body.decode("utf-8"))
                if isinstance(data, dict):
                    data["src"] = "plugin"
                    data.pop("channels", None)
                    body = json.dumps(data, ensure_ascii=False).encode("utf-8")
            except (ValueError, UnicodeDecodeError):
                pass
        return raw_path, body

    def _scrub_health(self, payload: bytes) -> bytes:
        try:
            data = json.loads(payload.decode("utf-8"))
            if isinstance(data, dict):
                data["channels"] = []
                data["channels_count"] = 0
                return json.dumps(data, ensure_ascii=False).encode("utf-8")
        except (ValueError, UnicodeDecodeError):
            pass
        return payload

    def _proxy(self) -> None:
        length = int(self.headers.get("Content-Length", "0") or "0")
        body = self.rfile.read(length) if length > 0 else None
        fwd_path, body = self._force_plugin_only(self.path, body)
        conn = http.client.HTTPConnection(
            BACKEND_HOST, BACKEND_PORT, timeout=self._timeout_for_path()
        )
        headers = {
            k: v
            for k, v in self.headers.items()
            if k.lower() not in SKIP_REQUEST_HEADERS
        }
        headers["Host"] = "%s:%s" % (BACKEND_HOST, BACKEND_PORT)
        headers["X-Forwarded-For"] = self.client_address[0]
        headers["X-Forwarded-Proto"] = "http"
        if body is not None:
            headers["Content-Length"] = str(len(body))
        try:
            conn.request(self.command, fwd_path, body=body, headers=headers)
            resp = conn.getresponse()
            payload = resp.read()
            if urlsplit(self.path).path == "/api/health":
                payload = self._scrub_health(payload)
            self.send_response(resp.status, resp.reason)
            for k, v in resp.getheaders():
                if k.lower() in SKIP_RESPONSE_HEADERS:
                    continue
                if k.lower() == "content-length":
                    continue
                self.send_header(k, v)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(payload)
        except Exception as exc:
            msg = ("bad gateway: %s\n" % exc).encode()
            self.send_response(502)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(msg)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(msg)
        finally:
            conn.close()

    def _static(self) -> None:
        full = safe_join(WEBROOT, self.path)
        if full is None:
            self.send_error(403)
            return
        if os.path.isdir(full):
            full = os.path.join(full, "index.html")
        if not os.path.isfile(full):
            index = os.path.join(WEBROOT, "index.html")
            if os.path.isfile(index) and self.command in ("GET", "HEAD"):
                full = index
            else:
                self.send_error(404)
                return
        try:
            with open(full, "rb") as fh:
                data = fh.read()
        except OSError:
            self.send_error(404)
            return
        ctype = self.guess_type(full)
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        if full.endswith(".html"):
            self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
        elif os.path.splitext(full)[1].lower() in {
            ".js",
            ".css",
            ".png",
            ".jpg",
            ".jpeg",
            ".gif",
            ".ico",
            ".svg",
            ".woff",
            ".woff2",
        }:
            self.send_header("Cache-Control", "public, max-age=2592000")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)

    @staticmethod
    def guess_type(path: str) -> str:
        ext = os.path.splitext(path)[1].lower()
        return {
            ".html": "text/html; charset=utf-8",
            ".js": "application/javascript; charset=utf-8",
            ".css": "text/css; charset=utf-8",
            ".json": "application/json; charset=utf-8",
            ".ico": "image/x-icon",
            ".png": "image/png",
            ".svg": "image/svg+xml",
            ".woff": "font/woff",
            ".woff2": "font/woff2",
        }.get(ext, "application/octet-stream")


def main() -> int:
    if not os.path.isdir(WEBROOT):
        sys.stderr.write("WEBROOT missing: %s\n" % WEBROOT)
        return 1
    httpd = ThreadingHTTPServer((LISTEN_HOST, LISTEN_PORT), Handler)
    sys.stderr.write(
        "gateway listening on %s:%s -> %s:%s web=%s\n"
        % (LISTEN_HOST, LISTEN_PORT, BACKEND_HOST, BACKEND_PORT, WEBROOT)
    )
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        httpd.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
