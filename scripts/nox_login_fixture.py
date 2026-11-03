"""Offline identity setup for disposable image acceptance tests only.

No fixture code/credentials are included in the production image. Signed OIDC,
PKCE and callback/browser binding are exercised by the Go integration tests.
"""
import datetime
import hashlib
import json
from pathlib import Path
import secrets
import shutil
import sqlite3

ISSUER = "https://auth.fixture.test"
SOCKET = "/run/nox-suite/acceptance-auth.sock"


def seed(data, origin, folder):
    """Call only after the disposable Backend is stopped (no SQLite writers)."""
    now = datetime.datetime.now(datetime.timezone.utc)
    stamp = lambda value: value.strftime("%Y-%m-%dT%H:%M:%SZ")
    config = dict(Issuer=ISSUER, Origin=origin, ClientID="backend-acceptance", Secret=secrets.token_urlsafe(32), SocketPath=SOCKET)
    result, tokens = {}, {}
    with sqlite3.connect(data / "nox-backend.db") as db, sqlite3.connect(data / "nox-login.db") as login:
        login.execute("INSERT OR REPLACE INTO config VALUES(1,?)", (json.dumps(config),))
        for kind, role in [("admin", "ADMIN"), ("member", "USER")]:
            user_id = "user_" + kind + "_fixture"
            email = kind + "@example.invalid"
            db.execute("INSERT INTO users (id,email,first_name,display_name,role,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)", (user_id,email,"",kind.title(),role,"ACTIVE",stamp(now),stamp(now)))
            key = "noxsync_" + secrets.token_urlsafe(32)
            digest = lambda value: hashlib.sha256(value.encode()).hexdigest()
            db.execute("INSERT INTO api_keys (user_id,key_prefix,key_value,key_hash,created_at) VALUES (?,?,?,?,?)", (user_id,"noxsync_",key,digest(key),stamp(now)))
            token, browser = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
            identity = dict(sub="fixture-"+kind,email=email,email_verified=True,name=kind.title(),nox_admin=True,google_sub="google-"+kind,google_iss="https://accounts.google.com")
            credentials = dict(AccessToken=token,Identity=identity,ExpiresAt=stamp(now+datetime.timedelta(days=365)),BrowserRenewAt=stamp(now+datetime.timedelta(days=30)))
            login.execute("INSERT INTO sessions VALUES(?,?,?,?)", (digest(browser),user_id,json.dumps(credentials),int((now+datetime.timedelta(days=365)).timestamp())))
            login.execute("INSERT INTO identities VALUES(?,?,?,?,?)", (ISSUER,identity['sub'],user_id,email,kind=="admin"))
            tokens[token] = identity
            result[kind+"Session"] = browser
            if kind == "admin":
                result["apiKey"] = key
                result["vaultId"] = "vault_fixture"
                db.execute("INSERT INTO vaults (id,user_id,name,revision,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", (result["vaultId"],user_id,"Notes",9007199254740993,"ACTIVE",stamp(now),stamp(now)))
    path = folder / "auth-provider.json"
    path.write_text(json.dumps(dict(client=config["ClientID"],secret=config["Secret"],tokens=tokens)))
    path.chmod(0o600)
    shutil.copyfile(Path(__file__).parent / "fixtures" / "nox-login-provider.py", folder / "auth-provider.py")
    return result


def start_provider(docker, folder, volume, uid):
    return docker("run","-d","--rm","--user",uid,"--group-add","10001","--mount",f"type=volume,source={volume},target=/run/nox-suite","--mount",f"type=bind,source={folder},target=/fixture","python:3.14-alpine","python","/fixture/auth-provider.py")
