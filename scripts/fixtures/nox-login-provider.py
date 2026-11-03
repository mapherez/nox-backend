"""Controlled private Auth peer for disposable Backend image acceptance only."""
import base64
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
import socket
import time
from urllib.parse import parse_qs

config = json.load(open('/fixture/auth-provider.json'))


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # Do not log credentials or URLs.

    def do_POST(self):
        expected = 'Basic ' + base64.b64encode((config['client']+':'+config['secret']).encode()).decode()
        if self.headers.get('Authorization') != expected:
            self.send_error(401)
            return
        body = self.rfile.read(int(self.headers.get('Content-Length', '0')))
        if self.path == '/v1/admission':
            result = {'allowed': json.loads(body)['email'] in {'admin@example.invalid','member@example.invalid'}}
        elif self.path == '/oauth/introspect':
            token = parse_qs(body.decode()).get('token',[''])[0]
            identity = config['tokens'].get(token)
            result = {**identity,'active':True,'client_id':config['client'],'exp':int(time.time())+365*86400} if identity else {'active':False}
        elif self.path == '/v1/logout':
            config['tokens'].pop(json.loads(body)['token'],None)
            self.send_response(204)
            self.end_headers()
            return
        else:
            self.send_error(404)
            return
        encoded = json.dumps(result).encode()
        self.send_response(200)
        self.send_header('Content-Type','application/json')
        self.send_header('Content-Length',str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


class UnixHTTPServer(HTTPServer):
    address_family = socket.AF_UNIX

    def server_bind(self):
        self.socket.bind(self.server_address)
        self.server_name, self.server_port = 'fixture', 0


path = '/run/nox-suite/acceptance-auth.sock'
server = UnixHTTPServer(path,Handler)
os.chmod(path,0o660)
print('auth fixture ready',flush=True)
server.serve_forever()
