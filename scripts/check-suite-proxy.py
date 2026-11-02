"""Validate proxy isolation and the same-host Suite channel using disposable data."""
import argparse
import datetime
import hashlib
import sqlite3
import json
import os
import re
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def docker(*args):
    return subprocess.check_output(["docker", *map(str, args)], text=True).strip()


def request(base, path, context=None, headers=None, data=None):
    req = urllib.request.Request(base + path, headers=headers or {}, data=data)
    try:
        with urllib.request.urlopen(req, context=context, timeout=5) as response:
            return response.status, response.headers, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.headers, error.read()


def ready(base, path, context=None):
    end = time.monotonic() + 40
    while time.monotonic() < end:
        try:
            if request(base, path, context)[0] == 200:
                return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.25)
    raise AssertionError("Disposable service did not become ready")


def frontend_only(image):
    """Validate the frontend image without building or running a Backend."""
    container = docker("run", "-d", "--rm", "-p", "127.0.0.1::8080", image)
    try:
        base = "http://" + docker("port", container, "8080/tcp")
        ready(base, "/healthz")
        status, _, body = request(base, "/vault-dashboard")
        assert status == 200
        assets = re.findall(r'(?:src|href)="(/vault-dashboard/assets/[^"?]+)', body.decode())
        assert assets, "Dashboard does not reference built assets"
        for asset in assets:
            assert request(base, asset)[0] == 200, asset
        status, headers, body = request(base, "/api/nox/keys", headers={"X-Forwarded-Proto": "https", "X-Nox-Suite-Transport": "https"})
        assert status == 403 and json.loads(body)["error"]["code"] == "FORBIDDEN"
        assert headers["Cache-Control"] == "no-store"
        status, headers, body = request(base, "/mcp/suite", headers={"X-Forwarded-Proto": "https", "X-Nox-Suite-Transport": "https"}, data=b"{}")
        assert status == 403 and json.loads(body)["code"] == "FORBIDDEN"
        assert headers["Cache-Control"] == "no-store"
        print("PASS: frontend-only startup, dashboard/assets and rejected forged TLS claims; no Backend image required.")
    except Exception:
        subprocess.run(["docker", "logs", "--tail", "30", container], check=False)
        raise
    finally:
        subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--browser", action="store_true")
    parser.add_argument("--backend-image")
    parser.add_argument("--frontend-only", action="store_true")
    parser.add_argument("--frontend-image", required=True)
    args = parser.parse_args()
    if args.frontend_only:
        frontend_only(args.frontend_image)
        return
    if not args.backend_image:
        parser.error("--backend-image is required unless --frontend-only is selected")
    network = "nox-suite-check-" + secrets.token_hex(6)
    volume = network + "-local"
    containers = []
    with tempfile.TemporaryDirectory(prefix="nox-suite-local-check-") as temporary:
        folder = Path(temporary).resolve()
        data = folder / "data"
        data.mkdir()
        import shutil
        shutil.copyfile(Path(__file__).resolve().parent / "fixtures" / "suite-local-participant.py", folder / "participant.py")
        docker("network", "create", network)
        docker("volume", "create", volume)
        try:
            frontend = docker("run", "-d", "--rm", "--network", network, "-p", "127.0.0.1::8080", args.frontend_image)
            containers.append(frontend)
            frontend_address = docker("port", frontend, "8080/tcp")
            base = "http://" + frontend_address
            browser_base = "http://nox-suite.test:" + frontend_address.rsplit(":", 1)[1]
            ready(base, "/healthz")
            backend_args = ["run", "-d", "--rm", "--network", network, "--network-alias", "backend", "--user", f"{os.getuid()}:{os.getgid()}", "--group-add", "10001", "-p", "127.0.0.1::8080", "--mount", f"type=bind,source={data},target=/data", "--mount", f"type=volume,source={volume},target=/run/nox-suite", "-e", "NOX_BACKEND_PUBLIC_URL=" + browser_base, "-e", "NOX_BACKEND_SUITE_MCP_ENABLED=true", "-e", "NOX_BACKEND_ADMIN_EMAILS=admin@example.invalid", args.backend_image]
            backend = docker(*backend_args)
            containers.append(backend)
            backend_base = "http://" + docker("port", backend, "8080/tcp")
            ready(backend_base, "/v1/health")
            # Seed only with Backend stopped. Host/VM SQLite writers never overlap.
            docker("stop", backend)
            containers.remove(backend)
            db = sqlite3.connect(data / "nox-backend.db")
            admin_id = db.execute("SELECT id FROM users WHERE email='admin@example.invalid'").fetchone()[0]
            api_key = db.execute("SELECT key_value FROM api_keys WHERE user_id=?", (admin_id,)).fetchone()[0]
            session = secrets.token_urlsafe(32)
            now = datetime.datetime.now(datetime.timezone.utc)
            stamp = lambda value: value.strftime("%Y-%m-%dT%H:%M:%SZ")
            db.execute("INSERT INTO web_sessions VALUES (?,?,?,?,?)", ("websess_suite_browser", admin_id, hashlib.sha256(session.encode()).hexdigest(), stamp(now + datetime.timedelta(hours=1)), stamp(now)))
            db.commit()
            db.close()
            backend = docker(*backend_args)
            containers.append(backend)
            backend_base = "http://" + docker("port", backend, "8080/tcp")
            ready(backend_base, "/v1/health")
            participant = docker("run", "-d", "--rm", "--user", f"{os.getuid()}:{os.getgid()}", "--group-add", "10001", "--mount", f"type=volume,source={volume},target=/run/nox-suite", "--mount", f"type=bind,source={folder},target=/fixture", "python:3.14-alpine", "python", "/fixture/participant.py")
            containers.append(participant)
            end = time.monotonic() + 30
            while time.monotonic() < end:
                if "fixture ready" in docker("logs", participant):
                    break
                time.sleep(0.2)
            else:
                raise AssertionError("Local protocol participant did not start")
            keys = json.loads(docker("exec", participant, "python", "/fixture/participant.py", "--keys"))
            assert keys["contractVersion"] == "1.0" and keys["issuer"]
            for endpoint in [base, backend_base]:
                for path in ["/api/nox/keys", "/api/nox/products", "/api/nox/events?after=invalid&waitMs=0"]:
                    status, headers, body = request(endpoint, path, headers={"X-Forwarded-Proto": "https", "X-Nox-Suite-Transport": "https", "Authorization": "Bearer " + api_key})
                    assert status == 403 and json.loads(body)["error"]["code"] == "FORBIDDEN", (status, body)
                    assert headers["Cache-Control"] == "no-store"
                status, headers, body = request(endpoint, "/mcp/suite", headers={"X-Forwarded-Proto": "https", "X-Nox-Suite-Transport": "https"}, data=b"{}")
                assert status == 403 and json.loads(body)["code"] == "FORBIDDEN"
            auth = {"Authorization": "Bearer " + api_key}
            status, _, body = request(base, "/api/admin/suite", headers=auth)
            config = json.loads(body)["configuration"]
            assert status == 200 and config["canManage"] and config["canEnroll"] and config["transport"] == "unix"
            status, _, body = request(base, "/api/admin/suite/operations/keys", headers=auth)
            assert status == 200 and json.loads(body) == keys
            status, _, _ = request(base, "/v1/auth/check", headers=auth)
            assert status == 200
            if args.browser:
                fixture = folder / "suite-browser.json"
                fixture.write_text(json.dumps({"base": browser_base, "adminSession": session, "statePath": str(folder / "participant-state.json")}))
                fixture.chmod(0o600)
                root = Path(__file__).resolve().parent.parent
                subprocess.run(["npx.cmd" if os.name == "nt" else "npx", "playwright", "test", "e2e/suite.spec.ts"], cwd=root / "frontend", env={**os.environ, "NOX_SUITE_FIXTURE": str(fixture)}, check=True)
            keys = json.loads(docker("exec", participant, "python", "/fixture/participant.py", "--keys"))
            docker("exec", backend, "/usr/local/bin/nox-backend", "healthcheck")
            docker("restart", backend)
            backend_base = "http://" + docker("port", backend, "8080/tcp")
            ready(backend_base, "/v1/health")
            after = json.loads(docker("exec", participant, "python", "/fixture/participant.py", "--keys"))
            assert after == keys, "restart reset issuer/key trust"
            print("PASS: same-host Unix channel, rejected TCP/proxy spoofing, authenticated HTTP administration, preserved native API, browser setup/recovery when requested, persistent authority after restart; disposable data only.")
        except Exception:
            for container in containers:
                subprocess.run(["docker", "logs", "--tail", "20", container], check=False)
            raise
        finally:
            for container in reversed(containers):
                subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
            docker("network", "rm", network)
            docker("volume", "rm", volume)


if __name__ == "__main__":
    main()
