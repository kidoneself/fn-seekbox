import importlib.util
import fcntl
import http.server
import threading
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("update_helper", ROOT / "app/bin/update-helper.py")
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


class UpdateHelperTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.old = self.release("1.6.0")
        self.new = self.release("1.6.1")
        helper.switch_current(self.root, str(self.old))
        self.state = dict(id="transaction1", release_id="release1", phase="prepared", target=str(self.new),
                          previous=str(self.old), version="1.6.1", from_version="1.6.0", sha256="test-sha")
        helper.write_json(self.root / "update-state.json", self.state)
        self.args = type("Args", (), dict(bootstrap="/fixed/bootstrap", health_url="http://localhost/api/health", health_socket=""))()

    def release(self, version):
        path = self.root / "releases" / (version + "-amd64-digest")
        (path / "bin").mkdir(parents=True)
        (path / "bin/seekbox").write_text("fixture")
        (path / "runtime.json").write_text(json.dumps(dict(version=version, data_schema=1)))
        return path

    def test_success_commits_and_records_event(self):
        with patch.object(helper, "restart") as restart, patch.object(helper, "wait_health", return_value=True):
            helper.run(self.root, self.state, self.args)
        self.assertEqual((self.root / "current").resolve(), self.new)
        self.assertEqual(self.state["phase"], "committed")
        self.assertEqual(restart.call_count, 1)
        event = json.loads((self.root / "events/transaction1-success.json").read_text())
        self.assertEqual(event["status"], "success")
        self.assertEqual(event["release_id"], "release1")

    def test_failed_health_rolls_back(self):
        sentinel = self.root / "user-data-sentinel"
        sentinel.write_text("keep account data")
        with patch.object(helper, "restart") as restart, patch.object(helper, "wait_health", side_effect=[False, True]):
            helper.run(self.root, self.state, self.args)
        self.assertEqual((self.root / "current").resolve(), self.old)
        self.assertEqual(self.state["phase"], "rolled_back")
        self.assertEqual(restart.call_count, 2)
        self.assertEqual(sentinel.read_text(), "keep account data")
        self.assertEqual(json.loads((self.root / "failed-release.json").read_text())["sha256"], "test-sha")

    def test_power_loss_recovery_before_start(self):
        helper.switch_current(self.root, str(self.new))
        self.state["phase"] = "installing"
        with patch.object(helper, "restart") as restart:
            helper.recover(self.root, self.state)
        restart.assert_not_called()
        self.assertEqual((self.root / "current").resolve(), self.old)
        self.assertEqual(self.state["phase"], "rolled_back")

    def test_bootstrap_fallback(self):
        self.state["previous"] = ""
        helper.recover(self.root, self.state)
        self.assertFalse((self.root / "current").exists())

    def test_invalid_target_is_rejected(self):
        self.state["target"] = str(self.root.parent)
        with patch.object(helper, "restart") as restart, self.assertRaises(ValueError):
            helper.run(self.root, self.state, self.args)
        restart.assert_not_called()
        self.assertEqual((self.root / "current").resolve(), self.old)

    def test_bootstrap_recovery_does_not_interrupt_active_helper(self):
        helper.switch_current(self.root, str(self.new))
        with open(self.root / "update.lock", "a+b") as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
            self.assertEqual(helper.main(["recover", "--root", str(self.root)]), 0)
        self.assertEqual((self.root / "current").resolve(), self.new)
        self.assertEqual(json.loads((self.root / "update-state.json").read_text())["phase"], "prepared")

    def test_event_recovery_preserves_id_timestamp_and_error(self):
        helper.save_state(self.root, self.state, "rolled_back", "bad candidate")
        helper.event(self.root, self.state, "rolled_back", "NEW_RUNTIME_FAILED")
        original = (self.root / "events/transaction1-rolled_back.json").read_bytes()
        self.state["event_written"] = False
        (self.root / "events/transaction1-rolled_back.json").unlink()
        helper.recover(self.root, self.state)
        self.assertEqual((self.root / "events/transaction1-rolled_back.json").read_bytes(), original)

    def test_cleanup_keeps_active_previous_and_unknown_directories(self):
        older = self.root / "releases/1.5.9-amd64-0123456789abcdef"
        older.mkdir()
        (older / "runtime.json").write_text(json.dumps(dict(version="1.5.9", arch="amd64", data_schema=1)))
        unknown = self.root / "releases/keep-my-files"
        unknown.mkdir()
        helper.cleanup_releases(self.root, self.state)
        self.assertFalse(older.exists())
        self.assertTrue(self.old.exists())
        self.assertTrue(self.new.exists())
        self.assertTrue(unknown.exists())

    def test_health_requires_matching_static_frontend(self):
        class Handler(http.server.BaseHTTPRequestHandler):
            static_ok = False
            def do_GET(self):
                if self.path.endswith("/api/health"):
                    body, content, status = b'{"version":"1.6.1"}', "application/json", 200
                elif self.path.endswith("/version.json") and self.static_ok:
                    body, content, status = b'{"version":"1.6.1"}', "application/json", 200
                elif self.static_ok:
                    body, content, status = b"<!doctype html><html></html>", "text/html", 200
                else:
                    body, content, status = b"not found", "text/plain", 404
                self.send_response(status)
                self.send_header("Content-Type", content)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            def log_message(self, *args):
                pass
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            url = "http://127.0.0.1:%d/app/fn-seekbox/api/health" % server.server_port
            self.assertFalse(helper.health(url, "", "1.6.1"))
            Handler.static_ok = True
            self.assertTrue(helper.health(url, "", "1.6.1"))
            self.assertFalse(helper.health(url, "", "1.6.2"))
        finally:
            server.shutdown()
            worker.join()
            server.server_close()

    def test_rollback_failure_keeps_recovery_state(self):
        with patch.object(helper, "restart"), patch.object(helper, "wait_health", return_value=False), self.assertRaises(RuntimeError):
            helper.run(self.root, self.state, self.args)
        self.assertEqual(self.state["phase"], "recovery_failed")
        self.assertEqual((self.root / "current").resolve(), self.old)


if __name__ == "__main__":
    unittest.main()
