#!/usr/bin/env python3
"""Run lifecycle regression checks locally; never connect to a NAS."""

import os
from pathlib import Path
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time
import unittest


PROJECT = Path(__file__).resolve().parents[2]


class LifecycleTests(unittest.TestCase):
    def _run_gateway_start_case(self, gateway_source, expect_success):
        """Exercise real startup functions and Unix HTTP with isolated children."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            package_var, app = root / "var", root / "app"
            package_var.mkdir()
            (app / "bin").mkdir(parents=True)
            (app / "web").mkdir()
            with socket.socket() as reserved:
                reserved.bind(("127.0.0.1", 0))
                port = reserved.getsockname()[1]
            # Only replace the fixed backend port to avoid touching any locally
            # running app; the startup and readiness functions remain unchanged.
            script = (PROJECT / "cmd/main").read_text(encoding="utf-8")
            self.assertEqual(script.count('BACKEND_PORT="${SEEKBOX_BACKEND_PORT:-18888}"'), 1)
            lifecycle = root / "main"
            lifecycle.write_text(script.replace('BACKEND_PORT="${SEEKBOX_BACKEND_PORT:-18888}"', f'BACKEND_PORT="{port}"'), encoding="utf-8")
            backend = app / "bin/seekbox"
            backend.write_text(f"#!{sys.executable}\n" + """
import os, pathlib
from http.server import BaseHTTPRequestHandler, HTTPServer
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != '/api/health':
            self.send_error(404)
            return
        if self.headers.get('X-Trim-Userid') == 'fn-seekbox-startup':
            pathlib.Path(os.environ['LIFECYCLE_HEALTH_MARKER']).touch()
        payload = b'{"status":"ok","plugins":[]}'
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
    def log_message(self, *args):
        pass
HTTPServer(('127.0.0.1', int(os.environ['PORT'])), Handler).serve_forever()
""", encoding="utf-8")
            backend.chmod(0o755)
            (app / "bin/gateway.py").write_text(gateway_source, encoding="utf-8")
            env = {
                **os.environ,
                "TRIM_PKGVAR": str(package_var),
                "TRIM_APPDEST": str(app),
                "TRIM_TEMP_LOGFILE": str(root / "failure.log"),
                "LIFECYCLE_REAL_GATEWAY": str(PROJECT / "app/bin/gateway.py"),
                "LIFECYCLE_HEALTH_MARKER": str(root / "gateway-health.ready"),
                "LIFECYCLE_SOCKET_MARKER": str(root / "socket-created"),
                "LIFECYCLE_MAIN": str(lifecycle),
                "LIFECYCLE_READY": str(root / "started"),
                "LIFECYCLE_STOP": str(root / "stop"),
                "NO_PROXY": "127.0.0.1,localhost",
            }
            # fnOS does not always supply PATH to package lifecycle scripts.
            env.pop("PATH", None)
            # Keep the owning shell alive until stop so it can reap its own
            # children even on containers whose PID 1 does not reap orphans.
            harness = """
source "$LIFECYCLE_MAIN" start || exit $?
touch "$LIFECYCLE_READY"
while [ ! -f "$LIFECYCLE_STOP" ]; do sleep 0.05; done
source "$LIFECYCLE_MAIN" stop
wait
"""
            started_at = time.monotonic()
            process = subprocess.Popen(["bash", "-c", harness], env=env, start_new_session=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                deadline = started_at + 12
                while process.poll() is None and not (root / "started").exists():
                    self.assertLess(time.monotonic(), deadline, "gateway start did not finish within the test budget")
                    if (root / "socket-created").exists() and not (root / "gateway-health.ready").exists():
                        self.assertFalse((root / "started").exists(), "socket existence alone was treated as readiness")
                    time.sleep(0.02)
                if expect_success:
                    self.assertTrue((root / "started").exists(), (root / "failure.log").read_text() if (root / "failure.log").exists() else "start exited unexpectedly")
                    self.assertGreaterEqual(time.monotonic() - started_at, 2.4, "the fixture did not exercise a real slow start")
                    self.assertTrue((root / "gateway-health.ready").exists(), "start succeeded without a real gateway health response")
                    self.assertTrue((app / "app.sock").is_socket())
                    (root / "stop").touch()
                    stdout, stderr = process.communicate(timeout=25)
                    self.assertEqual(process.returncode, 0, (stdout + stderr).decode())
                else:
                    stdout, stderr = process.communicate(timeout=10)
                    self.assertNotEqual(process.returncode, 0, "exited gateway was reported ready")
                    self.assertLess(time.monotonic() - started_at, 10, "an exited child consumed the full startup timeout")
                    self.assertIn("exited before gateway readiness", (package_var / "info.log").read_text())
                self.assertFalse((package_var / "gateway.pid").exists())
                self.assertFalse((package_var / "seekbox.pid").exists())
                self.assertFalse((app / "app.sock").exists())
            finally:
                (root / "stop").touch()
                if process.poll() is None:
                    try:
                        process.communicate(timeout=25)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL)
                        process.communicate(timeout=5)

    def test_start_waits_for_slow_gateway_and_real_health(self):
        self._run_gateway_start_case("""
import os, pathlib, runpy, time
# Delay before binding longer than the old one-second startup check.
time.sleep(1.3)
gateway = runpy.run_path(os.environ['LIFECYCLE_REAL_GATEWAY'])
gateway['remove_stale_socket']()
server = gateway['ThreadingUnixHTTPServer'](gateway['GATEWAY_SOCKET'], gateway['Handler'])
pathlib.Path(os.environ['LIFECYCLE_SOCKET_MARKER']).touch()
# Also leave a real bound socket temporarily unable to answer HTTP.
time.sleep(1.3)
server.serve_forever()
""", expect_success=True)

    def test_start_fails_promptly_if_gateway_exits(self):
        self._run_gateway_start_case("raise SystemExit(23)\n", expect_success=False)

    def test_reinstall_keeps_cloud_data_private_and_unchanged(self):
        for previous_dir_mode, previous_file_mode in ((0o700, 0o600), (0o755, 0o644)):
            with self.subTest(previous_mode=previous_file_mode), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                package_var = root / "var"
                cloud = package_var / "data" / "cloud"
                cloud.mkdir(parents=True)
                cloud.chmod(previous_dir_mode)
                for filename in ("credentials.key", "cloud.db"):
                    target = cloud / filename
                    target.write_bytes(b"existing application data")
                    target.chmod(previous_file_mode)
                app = root / "app"
                (app / "bin").mkdir(parents=True)
                for filename in ("seekbox", "gateway.py"):
                    (app / "bin" / filename).touch()
                env = {
                    **os.environ,
                    "TRIM_PKGVAR": str(package_var),
                    "TRIM_APPDEST": str(app),
                    "TRIM_RUN_USERNAME": "",
                }
                subprocess.run(["bash", str(PROJECT / "cmd/install_callback")], env=env, check=True, timeout=10)
                self.assertEqual(stat.S_IMODE(cloud.stat().st_mode), 0o700)
                for filename in ("credentials.key", "cloud.db"):
                    target = cloud / filename
                    self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
                    self.assertEqual(target.read_bytes(), b"existing application data")

    def test_stop_waits_for_backend_after_gateway_exits(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            package_var = root / "var"
            package_var.mkdir()
            app = root / "app"
            app.mkdir()
            child_code = """
import pathlib, signal, sys, threading, time
ready, finished, delay = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), float(sys.argv[3])
stopped = threading.Event()
signal.signal(signal.SIGTERM, lambda *_: stopped.set())
ready.touch()
stopped.wait()
time.sleep(delay)
finished.touch()
"""
            children = []
            reapers = []
            try:
                for role, delay, pid_name in (
                    ("gateway", "0", "gateway.pid"),
                    ("backend", "1.4", "seekbox.pid"),
                ):
                    child = subprocess.Popen(
                        [sys.executable, "-c", child_code, str(root / f"{role}.ready"), str(root / f"{role}.finished"), delay],
                        stdout=subprocess.DEVNULL,
                        stderr=subprocess.DEVNULL,
                    )
                    children.append(child)
                    (package_var / pid_name).write_text(str(child.pid), encoding="utf-8")
                    # Reap exited children promptly; kill -0 also sees zombies.
                    reaper = threading.Thread(target=child.wait, daemon=True)
                    reaper.start()
                    reapers.append(reaper)
                deadline = time.monotonic() + 5
                while not all((root / f"{role}.ready").exists() for role in ("gateway", "backend")):
                    self.assertLess(time.monotonic(), deadline, "test children did not start")
                    time.sleep(0.01)
                env = {
                    **os.environ,
                    "TRIM_PKGVAR": str(package_var),
                    "TRIM_APPDEST": str(app),
                    "TRIM_TEMP_LOGFILE": str(root / "failure.log"),
                }
                subprocess.run(["bash", str(PROJECT / "cmd/main"), "stop"], env=env, check=True, timeout=25)
                for reaper in reapers:
                    reaper.join(timeout=2)
                self.assertTrue((root / "gateway.finished").exists(), "gateway did not exit normally")
                self.assertTrue((root / "backend.finished").exists(), "backend was killed before its shutdown finished")
                self.assertTrue(all(child.returncode == 0 for child in children), "a child received forced termination")
                self.assertFalse((package_var / "gateway.pid").exists())
                self.assertFalse((package_var / "seekbox.pid").exists())
            finally:
                for child in children:
                    if child.poll() is None:
                        child.kill()
                    child.wait(timeout=5)


if __name__ == "__main__":
    unittest.main()
