"""Exercise disposable backend/frontend containers; never attaches installed data."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

import nox_login_fixture

ROOT = Path(__file__).resolve().parent.parent

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def docker(*args):
    return subprocess.check_output(["docker", *map(str,args)], text=True).strip()

def request(base, path, *, headers=None, data=None, method=None):
    req = urllib.request.Request(base+path, data=data, headers=headers or {}, method=method)
    try:
        with urllib.request.build_opener(NoRedirect).open(req, timeout=10) as response:
            return response.status, response.headers, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.headers, error.read()

def ready(base,path):
    end=time.monotonic()+40
    while time.monotonic()<end:
        try:
            if request(base,path)[0]==200: return
        except (OSError,urllib.error.URLError): pass
        time.sleep(0.25)
    raise AssertionError("Disposable service did not become ready")

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--backend-image',default='nox-backend:step2-validation')
    parser.add_argument('--frontend-image',default='nox-backend-frontend:step2-validation')
    parser.add_argument('--browser',action='store_true')
    args=parser.parse_args()
    network='nox-backend-check-'+secrets.token_hex(6)
    containers=[]
    volume=network+'-local'
    with tempfile.TemporaryDirectory(prefix='nox-backend-split-') as temporary:
        folder=Path(temporary).resolve(); data=folder/'data';data.mkdir()
        docker('network','create',network)
        docker('volume','create',volume)
        try:
            frontend=docker('run','-d','--rm','--network',network,'-p','127.0.0.1::8080',args.frontend_image)
            containers.append(frontend);base='http://'+docker('port',frontend,'8080/tcp');ready(base,'/healthz')
            backend_args=['run','-d','--rm','--network',network,'--network-alias','backend','--user',f'{os.getuid()}:{os.getgid()}','--group-add','10001','-p','127.0.0.1::8080','--mount',f'type=bind,source={data},target=/data','--mount',f'type=volume,source={volume},target=/run/nox-suite','-e','NOX_BACKEND_PUBLIC_URL='+base,'-e','NOX_AUTH_PUBLIC_URL='+nox_login_fixture.ISSUER,args.backend_image]
            backend=docker(*backend_args);containers.append(backend);backend_base='http://'+docker('port',backend,'8080/tcp');ready(backend_base,'/v1/health')
            docker('stop',backend);containers.remove(backend)
            seeded=nox_login_fixture.seed(data,base,folder)
            admin_session,member_session,api_key,vault_id=[seeded[key] for key in ['adminSession','memberSession','apiKey','vaultId']]
            member_id='user_member_fixture';admin_id='user_admin_fixture'
            provider=nox_login_fixture.start_provider(docker,folder,volume,f'{os.getuid()}:{os.getgid()}');containers.append(provider)
            for attempt in range(100):
                if 'auth fixture ready' in docker('logs',provider): break
                time.sleep(.1)
            else: raise AssertionError('Auth fixture did not start')
            backend=docker(*backend_args);containers.append(backend);backend_base='http://'+docker('port',backend,'8080/tcp');ready(backend_base,'/v1/health')
            docker('exec',backend,'/usr/local/bin/nox-backend','healthcheck')
            docker('exec',frontend,'wget','-q','-O','/dev/null','http://127.0.0.1:8080/healthz')
            auth={'Authorization':'Bearer '+api_key};cookie={'Cookie':'nox_sync_session='+admin_session}
            status,headers,body=request(base,'/vault-dashboard');assert status==200 and b'/vault-dashboard/assets/dashboard.js' in body
            assert headers['Cache-Control']=='no-store'
            for asset in ['dashboard.js','dashboard.css','favicon.svg']:
                status,_,body=request(base,'/vault-dashboard/assets/'+asset);assert status==200 and body,asset
            assert request(backend_base,'/vault-dashboard/assets/dashboard.js')[0]==404
            # Native TCP/proxy routes never provide the inter-product local channel.
            for endpoint in [base,backend_base]:
                status,headers,body=request(endpoint,'/api/nox/keys',headers={'X-Forwarded-Proto':'https','X-Nox-Suite-Transport':'https'})
                assert status==403 and json.loads(body)['error']['code']=='FORBIDDEN'
                assert headers['Cache-Control']=='no-store'

            status,headers,body=request(base,'/api/dashboard');view=json.loads(body);assert status==200 and not view['authenticated'] and not view['apiKey'];assert headers['Cache-Control']=='no-store'
            status,_,body=request(base,'/api/dashboard',headers=cookie);view=json.loads(body);assert status==200, ('dashboard HTTP',status);assert view['apiKey']==api_key, ('session/key projection',view['authenticated']);assert view['isAdmin'], 'admin projection';assert view['serverUrl']==base, ('public origin',view['serverUrl'],base)
            status,_,body=request(base,'/api/dashboard',headers={**cookie,'X-Forwarded-Proto':'https'});assert json.loads(body)['serverUrl']==base
            status,headers,_=request(base,'/auth/google/start?redirect=/vault-dashboard',headers={'X-Forwarded-Proto':'https'});assert status==303
            query=urllib.parse.parse_qs(urllib.parse.urlparse(headers['Location']).query);assert query['redirect_uri']==[base+'/auth/nox/callback'];assert headers['Location'].startswith(nox_login_fixture.ISSUER+'/authorize?')
            status,_,body=request(base,'/api/dashboard',headers=cookie);assert json.loads(body)['vaults'][0]['revision']=='9007199254740993'
            params=urllib.parse.urlencode({'email':'form-created@example.invalid','role':'USER'}).encode()
            status,headers,_=request(base,'/vault-dashboard/users/add',headers={**cookie,'Content-Type':'application/x-www-form-urlencoded'},data=params);assert status==303 and headers['Location']=='/vault-dashboard?message=user-saved'
            member_cookie={'Cookie':'nox_sync_session='+member_session}
            status,_,body=request(base,'/api/dashboard',headers=member_cookie);member=json.loads(body);assert status==200 and not member['isAdmin'] and not member['users'] and member['user']['id']==member_id
            status,_,body=request(base,'/vault-dashboard/users/delete',headers={**member_cookie,'Content-Type':'application/x-www-form-urlencoded'},data=urllib.parse.urlencode({'userId':admin_id}).encode());assert status==403
            status,_,body=request(base,'/v1/status?vaultId='+vault_id,headers={'Authorization':'Bearer '+member['apiKey']});assert status==404
            with urllib.request.urlopen(urllib.request.Request(base+'/v1/sync/events?'+urllib.parse.urlencode({'vaultId':vault_id,'api_key':api_key})),timeout=5) as response:
                assert response.headers['Content-Type']=='text/event-stream';assert response.readline()==b'event: status\n';assert response.readline().startswith(b'data: ')
            def rpc(identifier,method,params,authenticated=False):
                status,_,body=request(base,'/mcp',headers={'Content-Type':'application/json','Accept':'application/json, text/event-stream',**(auth if authenticated else {})},data=json.dumps({'jsonrpc':'2.0','id':identifier,'method':method,'params':params}).encode());assert status==200;return json.loads(body)['result']
            rpc(1,'initialize',{'protocolVersion':'2025-11-25','capabilities':{},'clientInfo':{'name':'split-validation','version':'test'}})
            assert len(rpc(2,'tools/list',{})['tools'])==10
            assert rpc(3,'tools/call',{'name':'backend_vault_list','arguments':{}})['isError']
            assert rpc(4,'tools/call',{'name':'backend_whoami','arguments':{}},True)['structuredContent']['user']=='admin@example.invalid'
            logs=subprocess.check_output(['docker','logs',frontend],stderr=subprocess.STDOUT,text=True)
            assert api_key not in logs and member['apiKey'] not in logs, 'proxy logs disclosed credentials'
            fixture={'base':base,'adminSession':admin_session,'memberSession':member_session,'apiKey':api_key,'vaultId':vault_id}
            if args.browser:
                fixture_path=folder/'fixture.json';fixture_path.write_text(json.dumps(fixture));fixture_path.chmod(0o600)
                subprocess.run(['npm.cmd' if os.name=='nt' else 'npm','run','test:e2e'],cwd=ROOT/'frontend',env={**os.environ,'NOX_SPLIT_FIXTURE':str(fixture_path)},check=True)
                api_key=json.loads(fixture_path.read_text())['apiKey']
            backend_before=docker('inspect','--format','{{.Id}}',backend)
            status,_,body=request(base,'/v1/vaults',headers={'Authorization':'Bearer '+api_key});assert status==200;vault_state=json.loads(body)
            # Replacing only the frontend must retain the backend and its durable state.
            docker('rm','-f',frontend);containers.remove(frontend)
            replacement=docker('run','-d','--rm','--network',network,'-p',base[len('http://'):]+':8080',args.frontend_image);containers.append(replacement)
            new_base='http://'+docker('port',replacement,'8080/tcp');ready(new_base,'/healthz')
            status,_,body=request(new_base,'/v1/auth/check',headers={'Authorization':'Bearer '+api_key});assert status==200
            assert new_base==base, 'frontend replacement changed the public origin'
            status,_,body=request(new_base,'/v1/vaults',headers={'Authorization':'Bearer '+api_key});assert status==200 and json.loads(body)==vault_state
            assert docker('inspect','--format','{{.Id}}',backend)==backend_before
            print('PASS: split images, dashboard/assets, cookies and role isolation, public origin/OAuth redirect, vaults/int64, native forms, API/MCP, unbuffered sync SSE, frontend-only replacement; temporary data only.')
        finally:
            for container in reversed(containers):
                subprocess.run(['docker','rm','-f',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=False)
            docker('network','rm',network)
            docker('volume','rm',volume)

if __name__=='__main__':main()
