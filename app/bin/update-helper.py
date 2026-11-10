#!/usr/bin/env python3
"""Fixed fn-seekbox bootstrap helper. Never loaded from a downloaded release."""
import argparse
import datetime
import fcntl
import http.client
import json
import os
import re
import shutil
from pathlib import Path
import socket
import subprocess
import sys
import time
import urllib.parse

PENDING = {"prepared", "installing", "rolling_back", "recovery_failed"}


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")


def sync_dir(path):
    fd = os.open(str(path), os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def write_json(path, value):
    tmp = path.with_name(path.name + ".tmp-" + str(os.getpid()))
    try:
        with open(tmp, "w", encoding="utf-8") as out:
            os.chmod(tmp, 0o600)
            json.dump(value, out, ensure_ascii=False, separators=(",", ":"))
            out.flush()
            os.fsync(out.fileno())
        os.replace(tmp, path)
        sync_dir(path.parent)
    finally:
        tmp.unlink(missing_ok=True)


def validated_target(root, target):
    if not target:
        return None
    path = Path(target)
    releases = (root / "releases").resolve()
    if not path.is_absolute() or path.resolve() != path or path.parent != releases:
        raise ValueError("invalid runtime release path")
    if not path.is_dir() or not (path / "bin/seekbox").is_file():
        raise ValueError("runtime release is incomplete")
    return path


def switch_current(root, target):
    path = validated_target(root, target)
    current = root / "current"
    if current.exists() and not current.is_symlink():
        raise ValueError("runtime current must be a symbolic link")
    if path is None:
        current.unlink(missing_ok=True)
    else:
        tmp = root / (".current-" + str(os.getpid()))
        tmp.unlink(missing_ok=True)
        os.symlink(str(path), str(tmp))
        os.replace(tmp, current)
    sync_dir(root)


def save_state(root, state, phase, error=""):
    if state.get("phase") != phase:
        state.pop("terminal_event", None)
        state.pop("event_written", None)
    state.update(phase=phase, error=error, updated_at=now())
    write_json(root / "update-state.json", state)


def event(root, state, status, error_code=""):
    folder = root / "events"
    folder.mkdir(mode=0o700, exist_ok=True)
    value = state.get("terminal_event")
    if not value:
        # Timestamp comes from the durable phase transition, not a retry's clock.
        # A later recovery after rollback_failed is a different immutable event.
        value = dict(event_id=state["id"] + "-" + status, release_id=state.get("release_id", ""),
                     version=state["version"], from_version=state["from_version"], status=status,
                     error_code=error_code, occurred_at=state["updated_at"])
        state["terminal_event"] = value
        write_json(root / "update-state.json", state)
    write_json(folder / (value["event_id"] + ".json"), value)
    state["event_written"] = True
    write_json(root / "update-state.json", state)


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, socket_path):
        super().__init__("localhost", timeout=3)
        self.socket_path = socket_path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.socket_path)


def health(url, socket_path, expected_version):
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "http" or parsed.hostname not in {"localhost", "127.0.0.1", "::1"}:
        raise ValueError("health endpoint must be local HTTP")

    def fetch(path, limit=65536):
        conn = UnixConnection(socket_path) if socket_path else http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=3)
        try:
            conn.request("GET", path, headers={"X-Trim-Userid": "fn-seekbox-updater"})
            response = conn.getresponse()
            body = response.read(limit + 1)
            return response.status, response.getheader("Content-Type", ""), body
        finally:
            conn.close()

    try:
        status, _, body = fetch(parsed.path or "/")
        if status != 200 or len(body) > 65536:
            return False
        data = json.loads(body)
        if not isinstance(data, dict) or data.get("version") != expected_version:
            return False
        prefix = parsed.path[:-len("/api/health")] if parsed.path.endswith("/api/health") else ""
        status, _, body = fetch(prefix + "/version.json")
        if status != 200 or len(body) > 65536 or json.loads(body).get("version") != expected_version:
            return False
        status, content_type, body = fetch(prefix + "/")
        return status == 200 and "text/html" in content_type and b"<html" in body.lower()
    except (OSError, ValueError, AttributeError, http.client.HTTPException):
        return False


def wait_health(url, socket_path, version, timeout=90):
    if os.environ.get("SEEKBOX_UPDATE_ALLOW_LOOPBACK_HTTP") == "1":
        timeout = max(3, int(os.environ.get("SEEKBOX_UPDATE_TEST_HEALTH_TIMEOUT", timeout)))
    deadline = time.monotonic() + timeout
    # Require more than a momentary successful response before committing.
    successes = 0
    while time.monotonic() < deadline:
        if health(url, socket_path, version):
            successes += 1
            if successes >= 3:
                return True
        else:
            successes = 0
        time.sleep(1)
    return False


def restart(bootstrap):
    command = Path(bootstrap)
    if not command.is_absolute() or not command.is_file():
        raise ValueError("fixed bootstrap is unavailable")
    # No release-controlled command, shell interpolation, or package install hooks.
    result = subprocess.run(["/bin/bash", str(command), "restart"], timeout=150, check=False)
    if result.returncode != 0:
        raise RuntimeError("bootstrap restart failed")


def recover(root, state):
    if state.get("phase") not in PENDING:
        if state.get("phase") in {"committed", "rolled_back"} and not state.get("event_written"):
            event(root, state, "success" if state["phase"] == "committed" else "rolled_back")
        return
    try:
        switch_current(root, state.get("previous", ""))
        save_state(root, state, "rolled_back", "Interrupted update restored before startup")
        event(root, state, "rolled_back", "INTERRUPTED_UPDATE")
    except Exception:
        save_state(root, state, "recovery_failed", "Could not restore previous runtime")
        raise


def run(root, state, args):
    if state.get("phase") != "prepared":
        raise ValueError("no prepared update transaction")
    validated_target(root, state["target"])
    validated_target(root, state.get("previous", ""))
    metadata = json.loads((Path(state["target"]) / "runtime.json").read_text())
    if metadata.get("version") != state["version"] or metadata.get("data_schema") != 1:
        raise ValueError("release metadata mismatch")
    try:
        save_state(root, state, "installing")
        switch_current(root, state["target"])
        restart(args.bootstrap)
        if not wait_health(args.health_url, args.health_socket, state["version"]):
            raise RuntimeError("new version health check failed")
        save_state(root, state, "committed")
        event(root, state, "success")
        try:
            cleanup_releases(root, state)
        except OSError:
            pass
    except Exception as exc:
        if state.get("phase") == "committed":
            print("Committed update reporting will resume at startup", file=sys.stderr)
            return
        write_json(root / "failed-release.json", {"sha256": state.get("sha256", ""), "release_id": state.get("release_id", ""), "at": now()})
        save_state(root, state, "rolling_back", str(exc))
        try:
            switch_current(root, state.get("previous", ""))
            restart(args.bootstrap)
            if not wait_health(args.health_url, args.health_socket, state["from_version"]):
                raise RuntimeError("previous version health check failed")
            save_state(root, state, "rolled_back", "New version failed; previous version restored")
            event(root, state, "rolled_back", "NEW_RUNTIME_FAILED")
        except Exception:
            if state.get("phase") == "rolled_back":
                print("Rollback reporting will resume at startup", file=sys.stderr)
                return
            save_state(root, state, "recovery_failed", "Rollback requires a successful application restart")
            event(root, state, "failed", "ROLLBACK_FAILED")
            raise


def cleanup_releases(root, state):
    """Remove only older updater-owned releases, preserving both rollback targets."""
    keep = {state.get("target"), state.get("previous")}
    current = root / "current"
    if current.is_symlink():
        keep.add(str(current.resolve()))
    pattern = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-(amd64|arm64)-[a-f0-9]{16}$")
    for path in (root / "releases").iterdir():
        if str(path) in keep or path.is_symlink() or not path.is_dir() or not pattern.fullmatch(path.name):
            continue
        try:
            metadata = json.loads((path / "runtime.json").read_text())
            expected = metadata["version"] + "-" + metadata["arch"] + "-"
            if metadata.get("data_schema") != 1 or not path.name.startswith(expected):
                continue
            # Only controlled release directories are traversed; user data is elsewhere.
            shutil.rmtree(path)
        except (OSError, ValueError, KeyError):
            # Cleanup failure must not turn a healthy committed update into a rollback.
            continue
    sync_dir(root / "releases")


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("run", "recover"))
    parser.add_argument("--root", "--runtime-root", dest="root", required=True)
    parser.add_argument("--bootstrap", default="")
    parser.add_argument("--health-url", default="")
    parser.add_argument("--health-socket", default="")
    args = parser.parse_args(argv)
    root = Path(args.root).resolve()
    root.mkdir(parents=True, exist_ok=True)
    with open(root / "update.lock", "a+b") as lock:
        try:
            mode = fcntl.LOCK_EX | (fcntl.LOCK_NB if args.command == "recover" else 0)
            fcntl.flock(lock.fileno(), mode)
        except BlockingIOError:
            # An active helper owns the transaction; bootstrap may start its candidate.
            return 0
        path = root / "update-state.json"
        if not path.exists():
            return 0 if args.command == "recover" else 1
        state = json.loads(path.read_text())
        if args.command == "recover":
            recover(root, state)
        else:
            run(root, state, args)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as error:
        print("update-helper:", str(error), file=sys.stderr)
        sys.exit(1)
