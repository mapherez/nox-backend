"""Run the shipped Backend Compose against a real, disposable NoX Auth image.

Only image names, isolated resource names and loopback test ports are replaced.
The actual setup command, dependency conditions, users and mounts are retained.
Never attaches an installed volume or contacts Google.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent.parent


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def request(base, path):
    try:
        with urllib.request.build_opener(NoRedirect).open(base+path,timeout=10) as r:
            return r.status,r.headers,r.read()
    except urllib.error.HTTPError as r:
        return r.code,r.headers,r.read()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--backend-image', required=True)
    parser.add_argument('--frontend-image', required=True)
    parser.add_argument('--auth-image', default='ghcr.io/mapherez/nox-auth:latest')
    args = parser.parse_args()
    project = 'nox-auth-acceptance-' + secrets.token_hex(6)
    with socket.socket() as listener:
        listener.bind(('127.0.0.1',0))
        port = listener.getsockname()[1]
    origin = f'http://127.0.0.1:{port}'
    issuer = 'https://auth.acceptance.test'
    env = {**os.environ, 'NOX_BACKEND_PUBLIC_URL':origin,'NOX_AUTH_PUBLIC_URL':issuer,'NOX_BACKEND_DATA_VOLUME_NAME':project+'-backend-data','NOX_SUITE_LOCAL_VOLUME_NAME':project+'-runtime','NOX_AUTH_ADMIN_VOLUME_NAME':project+'-admin'}
    # Prevent an operator's .env or optional overrides from entering acceptance.
    for name in list(env):
        if name.startswith(('NOX_BACKEND_','NOX_SYNC_','NOX_FRONTEND_')) and name not in {'NOX_BACKEND_PUBLIC_URL','NOX_BACKEND_DATA_VOLUME_NAME'}:
            del env[name]
    with tempfile.TemporaryDirectory(prefix=project) as temporary:
        folder = Path(temporary)
        def compose(file, name, *command, capture=False):
            return subprocess.run(['docker','compose','--env-file',os.devnull,'-f',str(file),'-p',name,*command],env=env,check=True,text=True,stdout=subprocess.PIPE if capture else None)
        auth_file = folder/'auth.json'
        auth_file.write_text(json.dumps({'services':{'auth':{'image':args.auth_image,'environment':{'NOX_AUTH_PUBLIC_URL':issuer,'NOX_AUTH_GOOGLE_CLIENT_ID':'unused-compose-check','NOX_AUTH_GOOGLE_CLIENT_SECRET':'unused-compose-check','NOX_AUTH_ALLOWED_EMAILS':'owner@example.invalid'},'volumes':['data:/data','runtime:/run/nox-suite','admin:/run/nox-auth-admin']}},'volumes':{'data':{},'runtime':{'name':env['NOX_SUITE_LOCAL_VOLUME_NAME']},'admin':{'name':env['NOX_AUTH_ADMIN_VOLUME_NAME']}}}))
        backend_file = folder/'backend.json'
        rendered = json.loads(compose(ROOT/'docker-compose.yml',project,'config','--format','json',capture=True).stdout)
        for name,service in rendered['services'].items():
            service.pop('container_name',None)
            service['image'] = args.frontend_image if name=='frontend' else args.backend_image
        rendered['services']['frontend']['ports'] = [{'target':8080,'published':str(port),'host_ip':'127.0.0.1','protocol':'tcp'}]
        backend_file.write_text(json.dumps(rendered))
        auth_started = backend_started = False
        try:
            auth_started = True
            compose(auth_file,project+'-auth','up','-d','--wait','--wait-timeout','60')
            backend_started = True
            compose(backend_file,project,'up','-d','--wait','--wait-timeout','120','--pull','never')
            # A volume already created by a different Compose project is reused.
            runtime = subprocess.check_output(['docker','volume','inspect',env['NOX_SUITE_LOCAL_VOLUME_NAME']],text=True)
            assert json.loads(runtime)[0]['Labels']['com.docker.compose.project']==project+'-auth'
            assert request(origin,'/healthz')[0]==200
            status,_,body = request(origin,'/api/dashboard')
            assert status==200 and json.loads(body)['oauthConfigured'] and not json.loads(body)['authenticated']
            status,headers,_ = request(origin,'/auth/nox/start?return_to=%2Fvault-dashboard%3Fview%3Dretained')
            assert status==303 and headers['Location'].startswith(issuer+'/authorize?')
            query = urllib.parse.parse_qs(urllib.parse.urlparse(headers['Location']).query)
            assert query['redirect_uri']==[origin+'/auth/nox/callback']
            client_id = query['client_id']
            assert headers['Set-Cookie'].find('HttpOnly')>=0 and 'Secure' not in headers['Set-Cookie']
            backend = compose(backend_file,project,'ps','-q','backend',capture=True).stdout.strip()
            mounts = json.loads(subprocess.check_output(['docker','inspect',backend],text=True))[0]['Mounts']
            assert '/run/nox-suite' in [m['Destination'] for m in mounts]
            assert '/run/nox-auth-admin' not in [m['Destination'] for m in mounts], 'runtime inherited enrollment authority'
            assert request(origin,'/api/nox/keys')[0]==403
            compose(backend_file,project,'run','--rm','--no-deps','auth-setup')
            compose(backend_file,project,'restart','backend')
            compose(backend_file,project,'up','-d','--wait','--wait-timeout','90','--pull','never')
            _,headers,_ = request(origin,'/auth/nox/start')
            assert urllib.parse.parse_qs(urllib.parse.urlparse(headers['Location']).query)['client_id']==client_id, 'setup/restart replaced enrolled client'
            print('PASS: shipped Compose, actual Auth enrollment/admission, cross-project volumes, private HTTP callback, idempotent setup/restart, admin mount absent at runtime. Real Google login remains operator acceptance.')
        except Exception:
            for file,name,started in [(backend_file,project,backend_started),(auth_file,project+'-auth',auth_started)]:
                if started:
                    subprocess.run(['docker','compose','-f',str(file),'-p',name,'logs','--tail','30'],check=False)
            raise
        finally:
            for file,name,started in [(backend_file,project,backend_started),(auth_file,project+'-auth',auth_started)]:
                if started:
                    compose(file,name,'down','-v','--remove-orphans','--timeout','5')


if __name__=='__main__':
    main()
