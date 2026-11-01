"""Validate packaged version/health contracts using disposable containers only."""
import argparse
import json
import subprocess
import time
import urllib.error
import urllib.request


def wait_for_health(url, *, attempts=60, delay=0.5, opener=urllib.request.urlopen, sleep=time.sleep):
    for _ in range(attempts):
        try:
            with opener(url, timeout=2) as response:
                assert response.status == 200
                return json.load(response)
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            # Docker can publish its port before the application accepts requests.
            # A reset/early disconnect is transient only during bounded readiness.
            sleep(delay)
    raise AssertionError("Backend did not become ready")


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--default-image")
    parser.add_argument("--versioned-image", required=True)
    parser.add_argument("--build-version", required=True)
    args = parser.parse_args()
    cases = [(args.default_image, None, None, "dev")] if args.default_image else []
    cases += [(args.versioned_image, canonical, legacy, expected) for canonical, legacy, expected in [
        (None, None, args.build_version), ("", "", args.build_version),
        (None, "runtime-test", "runtime-test"), ("canonical", None, "canonical"),
        ("canonical", "legacy", "canonical"), ("", "legacy", "legacy"),
    ]]
    for image, canonical, legacy, expected in cases:
        options = ["run", "--detach", "--rm", "--publish", "127.0.0.1::8080",
                   "--tmpfs", "/data:uid=10001,gid=10001,mode=0700"]
        for name, value in [("NOX_BACKEND_VERSION", canonical), ("NOX_SYNC_VERSION", legacy)]:
            if value is not None:
                options += ["--env", name + "=" + value]
        container = docker(*options, image)
        try:
            base = "http://" + docker("port", container, "8080/tcp")
            health = wait_for_health(base + "/v1/health")
            docker("exec", container, "/usr/local/bin/nox-backend", "healthcheck")
            for _ in range(60):
                status = docker("inspect", "--format", "{{.State.Health.Status}}", container)
                if status == "healthy":
                    break
                assert status != "unhealthy", "Docker healthcheck failed"
                time.sleep(0.5)
            else:
                raise AssertionError("Docker did not mark the container healthy")
            with urllib.request.urlopen(base + "/v1/info", timeout=2) as response:
                assert response.status == 200
                assert response.headers["Content-Type"] == "application/json"
                info = json.load(response)
            assert health == {"status": "ready", "version": expected,
                              "dataDirInitialized": True, "databasePath": "/data/nox-backend.db"}, health
            required = {"service": "nox-backend", "version": expected, "apiVersion": "v1",
                        "capabilities": ["auth", "vaults", "files", "vault-status"]}
            for field, value in required.items():
                assert field in info and type(info[field]) is type(value) and info[field] == value, (field, info)
            print(f"PASS: {image}, canonical={canonical!r}, legacy={legacy!r}, version={expected}", flush=True)
        except Exception:
            subprocess.run(["docker", "logs", container], check=False)
            raise
        finally:
            subprocess.run(["docker", "rm", "--force", container], check=False, stdout=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
