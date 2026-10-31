"""Validate actual TLS ingress/proxy boundaries using disposable images/data only."""
import argparse
import ipaddress
import json
import os
import re
from pathlib import Path
import secrets
import ssl
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
    raise AssertionError("Disposable TLS service did not become ready")


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
        print("PASS: frontend-only startup, dashboard/assets and rejected forged TLS claims; no Backend image required.")
    except Exception:
        subprocess.run(["docker", "logs", "--tail", "30", container], check=False)
        raise
    finally:
        subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--backend-image")
    parser.add_argument("--frontend-only", action="store_true")
    parser.add_argument("--frontend-image", required=True)
    args = parser.parse_args()
    if args.frontend_only:
        frontend_only(args.frontend_image)
        return
    if not args.backend_image:
        parser.error("--backend-image is required unless --frontend-only is selected")
    network = "nox-suite-proxy-check-" + secrets.token_hex(6)
    containers = []
    with tempfile.TemporaryDirectory(prefix="nox-suite-proxy-") as temporary:
        folder = Path(temporary).resolve()
        proxy_dir = folder / "proxy"
        proxy_dir.mkdir()
        data = folder / "data"
        data.mkdir()
        tls_data = folder / "tls-data"
        tls_data.mkdir()
        openssl = proxy_dir / "openssl.cnf"
        openssl.write_text("[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=v3\n[dn]\nCN=localhost\n[v3]\nsubjectAltName=IP:127.0.0.1,DNS:localhost\nbasicConstraints=critical,CA:TRUE\n")
        subprocess.run(["openssl", "req", "-x509", "-nodes", "-newkey", "rsa:2048", "-days", "1", "-config", str(openssl), "-keyout", str(proxy_dir / "key.pem"), "-out", str(proxy_dir / "cert.pem")], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        (proxy_dir / "key.pem").chmod(0o600)
        context = ssl.create_default_context(cafile=str(proxy_dir / "cert.pem"))
        (proxy_dir / "nginx.conf").write_text("""pid /tmp/nox-proxy.pid;
error_log /dev/stderr crit;
events { worker_connections 64; }
http {
 access_log off;
 client_body_temp_path /tmp/nox-client;
 proxy_temp_path /tmp/nox-proxy;
 fastcgi_temp_path /tmp/nox-fastcgi;
 uwsgi_temp_path /tmp/nox-uwsgi;
 scgi_temp_path /tmp/nox-scgi;
 server {
  listen 8443 ssl;
  ssl_certificate /fixture/cert.pem;
  ssl_certificate_key /fixture/key.pem;
  resolver 127.0.0.11 valid=1s ipv6=off;
  set $frontend http://frontend:8080;
  location = /healthz { return 200 'ready'; }
  location / {
   proxy_pass $frontend;
   proxy_set_header Host $http_host;
   proxy_set_header X-Forwarded-Proto $scheme;
   proxy_set_header X-Nox-Suite-Transport "";
  }
 }
}
""")
        docker("network", "create", network)
        try:
            info = json.loads(docker("network", "inspect", network))[0]
            subnet = ipaddress.ip_network(info["IPAM"]["Config"][0]["Subnet"])
            frontend_ip = str(subnet.network_address + min(200, subnet.num_addresses - 3))
            proxy = docker("run", "-d", "--rm", "--network", network, "--user", f"{os.getuid()}:{os.getgid()}", "-p", "127.0.0.1::8443", "--mount", f"type=bind,source={proxy_dir},target=/fixture,readonly", "--entrypoint", "nginx", args.frontend_image, "-g", "daemon off;", "-c", "/fixture/nginx.conf")
            containers.append(proxy)
            base = "https://" + docker("port", proxy, "8443/tcp")
            ready(base, "/healthz", context)
            proxy_ip = json.loads(docker("inspect", proxy))[0]["NetworkSettings"]["Networks"][network]["IPAddress"]
            backend = docker("run", "-d", "--rm", "--network", network, "--network-alias", "backend", "--user", f"{os.getuid()}:{os.getgid()}", "-p", "127.0.0.1::8080", "--mount", f"type=bind,source={data},target=/data", "-e", "NOX_BACKEND_PUBLIC_URL=" + base, "-e", "NOX_BACKEND_SUITE_TRUSTED_PROXY_CIDRS=" + frontend_ip + "/32", args.backend_image)
            containers.append(backend)
            backend_base = "http://" + docker("port", backend, "8080/tcp")
            ready(backend_base, "/v1/health")
            frontend = docker("run", "-d", "--rm", "--network", network, "--network-alias", "frontend", "--ip", frontend_ip, "-p", "127.0.0.1::8080", "-e", "NOX_FRONTEND_SUITE_TRUSTED_PROXY_CIDRS=" + proxy_ip + "/32", args.frontend_image)
            containers.append(frontend)
            frontend_base = "http://" + docker("port", frontend, "8080/tcp")
            ready(frontend_base, "/healthz")
            status, headers, body = request(base, "/api/nox/keys", context, {"X-Forwarded-Proto": "http", "X-Nox-Suite-Transport": "forged"})
            assert status == 200 and headers["Cache-Control"] == "no-store", (status, body)
            keys = json.loads(body)
            assert keys["contractVersion"] == "1.0" and keys["issuer"] and len(keys["keys"]) == 1
            for endpoint in [frontend_base, backend_base]:
                status, headers, body = request(endpoint, "/api/nox/keys", headers={"X-Forwarded-Proto": "https", "X-Nox-Suite-Transport": "https"})
                assert status == 403 and json.loads(body)["error"]["code"] == "FORBIDDEN"
                assert headers["Cache-Control"] == "no-store"
            status, _, body = request(base, "/api/nox/pairings", context, {"Content-Type": "application/json"}, b"{}")
            assert status == 401 and json.loads(body)["error"]["code"] == "UNAUTHENTICATED"
            for path in ["/api/nox/products", "/api/nox/events?after=invalid&waitMs=0"]:
                status, headers, body = request(base, path, context)
                assert status == 401 and json.loads(body)["error"]["code"] == "UNAUTHENTICATED"
                assert headers["Cache-Control"] == "no-store"
            docker("exec", backend, "/usr/local/bin/nox-backend", "healthcheck")
            docker("restart", backend)
            # Docker may reassign an ephemeral host port on restart. The service
            # alias/public TLS origin stay stable; refresh the diagnostic address.
            backend_base = "http://" + docker("port", backend, "8080/tcp")
            ready(backend_base, "/v1/health")
            ready(base, "/api/nox/keys", context)
            status, _, body = request(base, "/api/nox/keys", context)
            assert status == 200 and json.loads(body) == keys, "restart reset issuer/key trust"
            direct = docker("run", "-d", "--rm", "--network", network, "--user", f"{os.getuid()}:{os.getgid()}", "-p", "127.0.0.1::8080", "--mount", f"type=bind,source={tls_data},target=/data", "--mount", f"type=bind,source={proxy_dir},target=/fixture,readonly", "-e", "NOX_BACKEND_TLS_CERT_FILE=/fixture/cert.pem", "-e", "NOX_BACKEND_TLS_KEY_FILE=/fixture/key.pem", "-e", "NOX_BACKEND_TLS_CA_FILE=/fixture/cert.pem", args.backend_image)
            containers.append(direct)
            direct_base = "https://" + docker("port", direct, "8080/tcp")
            ready(direct_base, "/v1/health", context)
            docker("exec", direct, "/usr/local/bin/nox-backend", "healthcheck")
            print("PASS: trusted TLS ingress, sanitized proxy claims, rejected direct HTTP spoofing, JSON authentication boundary, persistent issuer after restart, direct Backend TLS and packaged HTTPS healthcheck; temporary data only.")
        except Exception:
            for container in containers:
                subprocess.run(["docker", "logs", "--tail", "30", container], check=False)
            raise
        finally:
            for container in reversed(containers):
                subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
            docker("network", "rm", network)


if __name__ == "__main__":
    main()
